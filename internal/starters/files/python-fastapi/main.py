"""An Aetherfy service agent: a FastAPI app exported as `app`.

Aetherfy imports this module and serves the object named `app` on port 8080.
Do not start a server yourself, and do not define GET /health: Aetherfy
answers that path in front of your app.
"""
import os

from fastapi import FastAPI

app = FastAPI()


@app.get("/")
def index():
    return {
        "message": "hello from Aetherfy",
        "agent": os.environ.get("AETHERFY_AGENT_NAME", "unknown"),
    }


@app.post("/echo")
def echo(body: dict):
    return {"you_sent": body}
