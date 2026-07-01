# audio-ripping-logchecker

A web UI to check EAC, XLD, dBpoweramp, and Whipper audio ripping logs and view the report online.

Built in Go using [logchecker-go](https://github.com/Nirzak/logchecker-go) natively.

## Run with Docker Compose (Recommended)

```bash
docker-compose up -d
```

The web app will be available at `http://localhost:5050`.

### Environment Variables

| Variable           | Default     | Description                                        |
|--------------------|-------------|----------------------------------------------------|
| `PUID`             | `1000`      | User ID to run the process as                      |
| `PGID`             | `1000`      | Group ID to run the process as                     |
| `SUBPATH`          | *(empty)*   | URL sub-path prefix, e.g. `/logchecker`            |
| `LOG_LEVEL`        | `error`     | Log verbosity: `debug`, `info`, `warn`, `error`    |
| `RATE_LIMIT_RPS`   | `0.5`       | Allowed requests per second per IP (0.5 = 30/min)  |
| `RATE_LIMIT_BURST` | `10`        | Maximum burst size for rate limiting               |
| `PORT`             | `5050`      | Port the server listens on                         |

## API Endpoint

Analyze log files programmatically via POST:

```bash
curl -X POST http://localhost:5050/api -F "logfile=@my_log.log"
```

**Response (JSON):**
```json
{
  "ripper": "EAC",
  "ripper_version": "1.3",
  "score": 100,
  "checksum_state": "checksum_ok",
  "details": null,
  "language": "en",
  "is_combined_log": false,
  "musicbrainz_id": "wIuHh1xPFhD4_OmVvz6lVMhdonE-",
  "musicbrainz_url": "https://musicbrainz.org/cdtoc/attach?toc=1+10+217190+183+21385+41203+64840+85335+107188+128210+146855+168838+187170&tracks=10&id=wIuHh1xPFhD4_OmVvz6lVMhdonE-",
  "ctdb_id": "LgiuDOWE08kAdzykzzIR3lULSi4-",
  "ctdb_url": "https://db.cuetools.net/ui/?tocid=LgiuDOWE08kAdzykzzIR3lULSi4-",
  "freedb_id": "970b4d0a",
  "accuraterip_id": "010-0011cd9b-008e7e63-970b4d0a",
  "accuraterip_status": "Found",
  "gnudb_id": "920b4d88",
  "gnudb_url": "https://gnudb.org/cd/920b4d88",
  "gnudb_status": "Matched",
  "gnudb_title": "Cutting Crew / Broadcast"
}
```

## Run without Docker

### Requirements

* Go 1.25+

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
