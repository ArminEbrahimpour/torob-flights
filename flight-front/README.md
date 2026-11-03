# Torob Flight Front

React/Vite frontend for the Torob flight MVP. It calls `core-api` through the
Nginx `/api` proxy and supports ordinary search plus smart-ranked results.

Start it after creating the shared network and core service:

```bash
docker network create torob-net 2>/dev/null || true
docker compose up -d --build
```

Open [http://localhost:3000](http://localhost:3000).
