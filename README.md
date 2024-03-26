# Livetap

> Microservice providing information about a given YouTube & Twitch live channels

## Requirements

- [Go](https://go.dev/dl)

## Setup

You'll need to get both create a Twitch and a YouTube application before proceeding.

Next, update the [data file](data.yaml), the `data` field is optional and used to attach miscellaneous data, such as social links for example.

## Usage

```sh
$ go run main.go --twitch.client_id=<TWITCH_CLIENT_ID> --twitch.client_secret=<TWITCH_CLIENT_SECRET> --youtube.api_key=<YOUTUBE_API_KEY>
```

## License

Copyright (c) 2023-present Alexandre Breteau

This software is released under the terms of the MIT License.
See the [LICENSE](LICENSE) file for further information.
