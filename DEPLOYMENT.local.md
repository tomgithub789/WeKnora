# WeKnora v0.7.0 isolated local deployment

This checkout is pinned to the official `v0.7.0` tag. The local override keeps
the stack separate from n8n:

- Compose project: `weknora070`
- Network: `weknora070_network`
- ParadeDB volume: `weknora070_postgres_data`
- Redis volume: `weknora070_redis_data`
- App data volume: `weknora070_app_data`
- Docreader temporary volume: `weknora070_docreader_tmp`
- UI: `http://127.0.0.1`
- API: `http://127.0.0.1:8080`

The wrapper creates deployment secrets under the ignored `.local-secrets`
directory with mode `0600`, injects them only into the Compose process, and
pins all WeKnora images to `v0.7.0`.

Use the wrapper for lifecycle commands:

```bash
./compose-isolated.sh ps
./compose-isolated.sh logs --tail=200
./compose-isolated.sh pull frontend app docreader postgres redis
./compose-isolated.sh up -d --no-build
./compose-isolated.sh down
```

Registration is initially enabled so the first local account can be created.
Both published ports are loopback-only. Do not add n8n networks, volumes,
databases, or containers to this project.
