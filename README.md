# audio-ripping-logchecker

A web UI to check EAC, XLD, dBpoweramp, and Whipper audio ripping logs and view the report online.

Built in Go using [logchecker-go](https://github.com/Nirzak/logchecker-go) natively — no external binary or subprocess required.

## Run with Docker Compose (Recommended)

```bash
docker-compose up -d
```

The web app will be available at `http://localhost:5050`.

### Environment Variables

| Variable     | Default          | Description                                        |
|--------------|------------------|----------------------------------------------------|
| `PUID`       | `1000`           | User ID to run the process as                      |
| `PGID`       | `1000`           | Group ID to run the process as                     |
| `SUBPATH`    | *(empty)*        | URL sub-path prefix, e.g. `/logchecker`            |
| `LOG_LEVEL`  | `error`          | Log verbosity: `debug`, `info`, `warning`, `error` |
| `RATE_LIMIT` | `30 per minute`  | Per-IP rate limit, e.g. `100 per minute`           |
| `PORT`       | `5050`           | Port the server listens on                         |

## API Endpoint

Analyze log files programmatically via POST:

```bash
curl -X POST http://localhost:5050/api -F "logfile=@my_log.log"
```

**Response (JSON):**
```json
{
  "ripper": "EAC",
  "ripper_version": "1.6",
  "score": 100,
  "checksum_state": "checksum_ok",
  "details": [],
  "language": "en",
  "is_combined_log": false
}
```

## Run without Docker

### Requirements

* Go 1.21+

### Build & Run

```bash
go build -o logchecker-web .
./logchecker-web
```

The server listens on `0.0.0.0:5050` by default.

## Nginx Reverse Proxy Config

```nginx
location /logchecker/ {
    proxy_pass http://127.0.0.1:5050/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

Set `SUBPATH=/logchecker` in the container environment to match.
