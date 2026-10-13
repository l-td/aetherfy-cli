// An Aetherfy service agent: a Hono app, exported as a fetch handler.
//
// An ES module ("type": "module" in package.json), exactly as Hono's own Node
// starter ships, minus its serve() call: Aetherfy serves the default export
// on port 8080. Do not call serve() or listen(), and do not define GET
// /health: Aetherfy answers that path in front of your app.
import { Hono } from 'hono';

const app = new Hono();

app.get('/', (c) =>
  c.json({
    message: 'hello from Aetherfy',
    agent: process.env.AETHERFY_AGENT_NAME ?? 'unknown',
  }),
);

app.post('/echo', async (c) => c.json({ you_sent: await c.req.json() }));

export default app;
