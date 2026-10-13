// An Aetherfy service agent: an Express app exported as `app`.
//
// Aetherfy requires this module and serves the object named `app` on port
// 8080. Do not call app.listen(), and do not define GET /health: Aetherfy
// answers that path in front of your app.
const express = require('express');

const app = express();
app.use(express.json());

app.get('/', (_req, res) => {
  res.json({
    message: 'hello from Aetherfy',
    agent: process.env.AETHERFY_AGENT_NAME || 'unknown',
  });
});

app.post('/echo', (req, res) => {
  res.json({ you_sent: req.body });
});

module.exports = { app };
