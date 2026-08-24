# lumo-movies-generator-by-actors

Standalone catalog-generator microservice for AuroraFlix — pulls a TMDB
actor's full movie credits into the shared `movies` catalog table (the same
one lumo-movies-svc owns). Rewritten from Java/Spring Boot to Go,
endpoint-for-endpoint and behavior-for-behavior compatible, to compare
real-world RAM usage on Render's free tier against the same rewrite already
done for lumo-movies-svc/lumo-series-svc.

Unlike those two services, this one has no auth middleware at all — the
Java source never had a SecurityConfig/GatewayAuthenticationFilter either,
since it isn't meant to be called through lumo-api-gateway.

## Run locally

```bash
go run ./cmd/server
```

Needs the same environment variables the Java service used: `DATABASE_URL`,
`DATABASE_USERNAME`, `DATABASE_PASSWORD`, `TMDB_API_KEY`, `TMDB_BASE_URL`,
and optionally `GRAFANA_OTLP_ENABLED` / `GRAFANA_OTLP_ENDPOINT` /
`GRAFANA_OTLP_AUTH`.

## Deploy

`Dockerfile` builds a static binary on a distroless base image — no JDK, no
Gradle. Render picks it up the same way it did the Java Dockerfile.

## Structure

- `cmd/server` — entrypoint, wiring
- `internal/generator` — the two endpoints (`/add-movies-by-actor/{imdb_id}`,
  `/search-movies`) and the whole TMDB-backed catalog pipeline (mirrors
  MoviesGeneratorByActorController + MoviesGeneratorByActorsService) —
  including the semaphore-limited (max 2 in flight) concurrent processing
  `AddMoviesByActor` uses, preserved as-is
- `internal/models`, `internal/actors`, `internal/tmdb`, `internal/dbx`,
  `internal/metrics`, `internal/reqlog`, `internal/config` — supporting
  pieces, each with a package doc comment pointing at the Java class it
  replaces. `actors` reads/writes the same shared `actors` table
  lumo-movies-svc/lumo-series-svc also use.
