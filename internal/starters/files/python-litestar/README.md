# Litestar on Aetherfy

A starter `service` agent written by `afy init --template python-litestar`. Runtime
`python3.12`, entrypoint `main.py`.

`requirements.txt` lists Litestar, an ASGI framework, so it is served like any other ASGI app; add your own dependencies there.

## Run it on Aetherfy

```bash
afy deploy
```

`afy deploy` offers to create the agent the first time. When it is live:

```bash
afy logs <agent-name>
```

The routes are `GET /` and `POST /echo` (JSON in, JSON out), plus a WebSocket echo at `/ws`. Aetherfy
answers `GET /health` itself, in front of your app.

## The contract

Aetherfy imports `main.py` and serves the object it exports as `app` (or as its default export). Keep that
export, and do not start a server yourself. Which frameworks fit, and how, is
at https://docs.aetherfy.com/agents/frameworks.
