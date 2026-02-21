# Lyra Discord Rich Presence
A Discord Rich Presence client implementation for Lyra, a work-in-progress and unreleased music server. Shows the currently playing track, album, artist, and cover art.

## Usage
```sh
go build && ./lyra-rpc
```
Optionally create a `config.json` in the working directory:
```json
{
  "base_url": "http://localhost:3000",
  "poll_interval_sec": 5
}
```

## License
This project is licensed under the [MPL-2.0](LICENSE.md). You are free to use this project as you see fit so long as you comply with the license's terms.
