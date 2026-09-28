# Express on Aetherfy

A starter `service` agent written by `afy init --template node-express`. Runtime
`node22`, entrypoint `index.js`.

`package.json` and `package-lock.json` pin Express; Aetherfy installs from the lockfile, so commit it after every `npm install`.

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

Aetherfy imports `index.js` and serves the object it exports as `app` (or as its default export). Keep that
export, and do not start a server yourself. Which frameworks fit, and how, is
at https://docs.aetherfy.com/agents/frameworks.
