"""An Aetherfy service agent: a Litestar app exported as `app`.

Litestar is an ASGI framework, so Aetherfy serves it exactly as it serves
FastAPI: it imports this module and serves the object named `app` on port
8080. Do not start a server yourself, and do not define GET /health.
"""
import os

from litestar import Litestar, WebSocket, get, post, websocket


@get("/")
async def index() -> dict:
    return {
        "message": "hello from Aetherfy",
        "agent": os.environ.get("AETHERFY_AGENT_NAME", "unknown"),
    }


@post("/echo", status_code=200)
async def echo(data: dict) -> dict:
    return {"you_sent": data}


@websocket("/ws")
async def ws_echo(socket: WebSocket) -> None:
    await socket.accept()
    async for message in socket.iter_data(mode="text"):
        await socket.send_data(message)


app = Litestar([index, echo, ws_echo])
