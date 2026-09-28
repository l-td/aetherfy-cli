# Hono (TypeScript, ES module) on Aetherfy

A starter `service` agent written by `afy init --template node-ts-hono`. Runtime
`node22-ts`, entrypoint `index.ts`.

An ES module (`"type": "module"`) exporting a Hono app as a fetch handler — the default export, with no `serve()` call. TypeScript runs with no build step. Aetherfy installs from `package-lock.json`, so commit it after every `npm install`.

## Run it on Aetherfy

```bash
afy deploy
```

`afy deploy` offers to create the agent the first time. When it is live:

```bash
afy logs <agent-name>
```

The routes are `GET /` and `POST /echo` (JSON in, JSON out). Aetherfy
answers `GET /health` itself, in front of your app.

## The contract

Aetherfy imports `index.ts` and serves the object it exports as `app` (or as its default export). Keep that
export, and do not start a server yourself. Which frameworks fit, and how, is
at https://docs.aetherfy.com/agents/frameworks.
