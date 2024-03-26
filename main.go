package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/nicklaw5/helix/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type MemberChannel struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type MemberStream struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	GameName    string    `json:"game_name"`
	URL         string    `json:"url"`
	EmbedURL    string    `json:"embed_url"`
	ViewerCount uint64    `json:"viewer_count"`
	StartedAt   time.Time `json:"started_at"`
}

type Member struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data"`

	Channels []*MemberChannel `json:"channels"`
	Streams  []*MemberStream  `json:"streams"`
}

type Data struct {
	Members []*Member `json:"-"`
}

var (
	ure = regexp.MustCompile(`(?i)rel="canonical" href="https://www\.youtube\.com/watch\?v=([^"]+)"`)
	gre = regexp.MustCompile(`(?i)"title":\{"simpleText":"([^"]+)"\},"subtitle"`)

	hc = http.Client{
		Timeout: 5 * time.Second,
	}

	data = &Data{}
)

func startWebServer(port int) error {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().
			Set("content-type", "application/json")

		json.NewEncoder(w).
			Encode(data.Members)
	})

	return http.ListenAndServe(fmt.Sprintf(":%d", port), handler)
}

func fetchLiveVideoID(channelID string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://www.youtube.com/channel/%s/live", channelID), nil)

	if err != nil {
		return "", err
	}

	req.AddCookie(&http.Cookie{
		Name:   "CONSENT",
		Value:  "YES+42",
		Secure: true,
	})

	resp, err := hc.Do(req)

	if err != nil {
		return "", err
	}

	bytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	if sm := ure.FindSubmatch(bytes); len(sm) > 0 {
		return string(sm[1]), nil
	}

	return "", nil
}

func fetchVideoGameName(videoID string) string {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID), nil)

	if err != nil {
		return ""
	}

	req.AddCookie(&http.Cookie{
		Name:   "CONSENT",
		Value:  "YES+42",
		Secure: true,
	})

	resp, err := hc.Do(req)

	if err != nil {
		return ""
	}

	bytes, err := io.ReadAll(resp.Body)

	if err != nil {
		return ""
	}

	if sm := gre.FindSubmatch(bytes); len(sm) > 0 {
		return string(sm[1])
	}

	return ""
}

func fetchVideos(yts *youtube.Service, videoIDs []string) ([]*youtube.Video, error) {
	if len(videoIDs) == 0 {
		return make([]*youtube.Video, 0), nil
	}

	resp, err := yts.Videos.List([]string{"snippet", "liveStreamingDetails"}).
		Id(videoIDs...).
		Do()

	if err != nil {
		return nil, err
	}

	return resp.Items, nil
}

func fetchStreams(hlc *helix.Client, userIDs []string) ([]helix.Stream, error) {
	if len(userIDs) == 0 {
		return make([]helix.Stream, 0), nil
	}

	resp, err := hlc.GetStreams(&helix.StreamsParams{
		UserIDs: userIDs,
		First:   100,
	})

	if err != nil {
		return nil, err
	}

	return resp.Data.Streams, nil
}

func refreshTwitchToken(hlc *helix.Client) error {
	resp, err := hlc.RequestAppAccessToken([]string{})

	if err == nil {
		hlc.SetAppAccessToken(resp.Data.AccessToken)
	}

	return err
}

func getMemberStreams(member *Member, streams []helix.Stream, videos []*youtube.Video) []*MemberStream {
	res := make([]*MemberStream, 0)

	for _, channel := range member.Channels {
		switch channel.Type {
		case "twitch":
			for _, stream := range streams {
				if stream.UserID != channel.Value {
					continue
				}

				res = append(res, &MemberStream{
					ID:   stream.ID,
					Type: channel.Type,

					Title:       stream.Title,
					GameName:    stream.GameName,
					URL:         fmt.Sprintf("https://twitch.tv/%s", stream.UserLogin),
					EmbedURL:    fmt.Sprintf("https://player.twitch.tv/?channel=%s", stream.UserLogin),
					ViewerCount: uint64(stream.ViewerCount),
					StartedAt:   stream.StartedAt,
				})
			}

		case "youtube":
			for _, video := range videos {
				if video.Snippet.ChannelId != channel.Value {
					continue
				}

				startedAt, _ := time.Parse(time.RFC3339, video.LiveStreamingDetails.ActualStartTime)

				if startedAt.IsZero() {
					continue
				}

				res = append(res, &MemberStream{
					ID:   video.Id,
					Type: channel.Type,

					Title:       video.Snippet.Title,
					GameName:    fetchVideoGameName(video.Id),
					URL:         fmt.Sprintf("https://youtube.com/watch?v=%s", video.Id),
					EmbedURL:    fmt.Sprintf("https://youtube.com/embed/%s", video.Id),
					ViewerCount: video.LiveStreamingDetails.ConcurrentViewers,
					StartedAt:   startedAt,
				})
			}
		}
	}

	return res
}

func refresh(ctx context.Context, yts *youtube.Service, hlc *helix.Client) error {
	if err := refreshTwitchToken(hlc); err != nil {
		return err
	}

	channelIDs := make([]string, 0)
	userIDs := make([]string, 0)

	for _, member := range data.Members {
		for _, channel := range member.Channels {
			switch channel.Type {
			case "twitch":
				userIDs = append(userIDs, channel.Value)

			case "youtube":
				channelIDs = append(channelIDs, channel.Value)
			}
		}
	}

	streams, err := fetchStreams(hlc, userIDs)

	if err != nil {
		return err
	}

	videoIDs := make([]string, 0)

	for _, channelID := range channelIDs {
		videoID, _ := fetchLiveVideoID(channelID)

		if videoID != "" {
			videoIDs = append(videoIDs, videoID)
		}
	}

	videos, err := fetchVideos(yts, videoIDs)

	if err != nil {
		return err
	}

	for _, member := range data.Members {
		member.Streams = getMemberStreams(member, streams, videos)
	}

	return nil
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out: os.Stdout,
	})

	app := &cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "twitch.client_id",
				EnvVars:  []string{"TWITCH_CLIENT_ID"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "twitch.client_secret",
				EnvVars:  []string{"TWITCH_CLIENT_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "youtube.api_key",
				EnvVars:  []string{"YOUTUBE_API_KEY"},
				Required: true,
			},
			&cli.StringFlag{
				Name:    "data.path",
				EnvVars: []string{"DATA_PATH"},
				Value:   "data.yaml",
			},
			&cli.IntFlag{
				Name:    "port",
				EnvVars: []string{"PORT"},
				Value:   3000,
			},
		},
		Action: func(ctx *cli.Context) error {
			config.AddDriver(yaml.Driver)

			if err := config.LoadFiles(ctx.String("data.path")); err != nil {
				return err
			}

			if err := config.Decode(data); err != nil {
				return err
			}

			hlc, err := helix.NewClient(&helix.Options{
				ClientID:     ctx.String("twitch.client_id"),
				ClientSecret: ctx.String("twitch.client_secret"),
			})

			if err != nil {
				return err
			}

			yts, err := youtube.NewService(ctx.Context, option.WithAPIKey(ctx.String("youtube.api_key")))

			if err != nil {
				return err
			}

			go startWebServer(ctx.Int("port"))

			for {
				if err := refresh(ctx.Context, yts, hlc); err != nil {
					log.Error().Err(err).Send()
				}

				time.Sleep(time.Minute)
			}
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Send()
	}
}
