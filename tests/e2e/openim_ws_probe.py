#!/usr/bin/env python3
"""Small stdin-driven OpenIM WebSocket kick probe for the opt-in live E2E."""
import asyncio
import sys

import websockets


async def main():
    url = sys.stdin.readline().strip()
    if not url:
        raise RuntimeError("missing WebSocket URL")
    async with websockets.connect(url, open_timeout=15, close_timeout=5) as ws:
        # OpenIM sends its handshake result before registering the client.
        await asyncio.wait_for(ws.recv(), timeout=15)
        print("ready", flush=True)
        try:
            message = await asyncio.wait_for(ws.recv(), timeout=20)
            print("kick_message_binary" if isinstance(message, bytes) else "kick_message_text", flush=True)
        except websockets.ConnectionClosed:
            print("kick_closed", flush=True)
        except TimeoutError:
            print("kick_timeout", flush=True)


asyncio.run(main())
