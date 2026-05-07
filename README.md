# Lyra Discord Rich Presence

A Discord Rich Presence client implementation for Lyra, a work-in-progress and unreleased music server. Shows the currently playing track, album, artist, and cover art.

## Usage

```sh
go build && ./lyra-rpc
```

Optionally create a `config.json` in the working directory:
```json
{
  "base_url": "http://localhost:4746",
  "auth_token": "",
  "poll_interval_sec": 5,
  "images": {
    "uploader": "none",
    "imgur_client_id": ""
  }
}
```

## License

This project is licensed under the [Lyra Public License, Version 1.0](LICENSE) (LPL-1.0). While this license is custom, it is based on the [MPL-2.0](https://opensource.org/license/MPL-2.0).

The main differences between the two are that the `LPL-1.0` includes an additional provision regarding Remote Network Interaction (inspired by the [AGPL-3.0](https://opensource.org/license/agpl-3-0)) and limits your secondary license options to only the `AGPL-3.0-or-later`.

You are free to use this project as you see fit, so long as you comply with the license's terms.
