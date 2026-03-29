#!/usr/bin/env python3
"""Hermès product tracker — Camoufox / Patchright with residential-proxy support."""

import json
import logging
import os
import re
import time
from dataclasses import dataclass
from typing import Any, Callable, Optional

from dotenv import load_dotenv
import httpx

log = logging.getLogger(__name__)

# ── constants ────────────────────────────────────────────────────────────────

HERMES_BASE = "https://www.hermes.com/tw/zh/"

BAG_PATTERN = re.compile(r"(?i).*(constance|lindy|kelly|picotin).*")

API_URLS = [
    "https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSSMALLLEATHER&sort=relevance&pagesize=40",
    "https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSBAGSCLUTCHES&sort=relevance&pagesize=40",
]

POLL_INTERVAL = 600  # seconds (10 minutes)

# Overridable in tests to point at a mock HTTP server.
sendgrid_host = "https://api.sendgrid.com"


# ── data model ───────────────────────────────────────────────────────────────

@dataclass
class Item:
    sku: str = ""
    title: str = ""
    avg_color: str = ""
    price: int = 0
    url: str = ""
    slug: str = ""


# ── pure helpers ─────────────────────────────────────────────────────────────

def parse_api_response(json_str: str) -> list[Item]:
    """Unmarshal a raw JSON string from the Hermès API."""
    data = json.loads(json_str)
    return [
        Item(
            sku=raw.get("sku", ""),
            title=raw.get("title", ""),
            avg_color=raw.get("avgColor", ""),
            price=raw.get("price", 0),
            url=raw.get("url", ""),
            slug=raw.get("slug", ""),
        )
        for raw in data.get("products", {}).get("items", [])
    ]


def filter_bags(items: list[Item]) -> list[Item]:
    """Return only items whose slug or title matches the target bag pattern."""
    return [
        item for item in items
        if BAG_PATTERN.match(item.slug) or BAG_PATTERN.match(item.title)
    ]


def item_product_url(item: Item) -> str:
    """Return the full product URL for an item."""
    return HERMES_BASE + item.url.lstrip("/")


def build_telegram_message(item: Item) -> str:
    """Return HTML-formatted Telegram notification for one item."""
    return (
        f"🛍 <b>Hermès 進貨通知</b>\n\n"
        f"<b>{item.title}</b>\n"
        f"顏色：{item.avg_color}\n"
        f"售價：NT${item.price}\n"
        f'<a href="{item_product_url(item)}">查看商品 →</a>'
    )


def build_email_content(item: Item) -> str:
    """Return plain-text email body for one item."""
    return (
        f"Hermès 進貨通知\n\n{item.title}\n"
        f"顏色：{item.avg_color}\n"
        f"售價：NT${item.price}\n"
        f"{item_product_url(item)}"
    )


# ── notification senders ─────────────────────────────────────────────────────

def send_email(content: str) -> None:
    """Send an email via the SendGrid v3 API.  No-op when SENDGRID_API_KEY is unset."""
    key = os.getenv("SENDGRID_API_KEY")
    if not key:
        return

    payload = {
        "personalizations": [
            {
                "to": [
                    {
                        "email": os.getenv("SEND_TO_ADDRESS", ""),
                        "name": os.getenv("SEND_TO_NAME", ""),
                    }
                ]
            }
        ],
        "from": {
            "email": os.getenv("SEND_FROM_ADDRESS", ""),
            "name": os.getenv("SEND_FROM_NAME", ""),
        },
        "subject": "Hermes 進貨囉",
        "content": [{"type": "text/plain", "value": content}],
    }

    resp = httpx.post(
        f"{sendgrid_host}/v3/mail/send",
        json=payload,
        headers={
            "Authorization": f"Bearer {key}",
            "Content-Type": "application/json",
        },
    )
    if resp.status_code < 200 or resp.status_code >= 300:
        raise RuntimeError(
            f"sendgrid status {resp.status_code}: {resp.text}"
        )


def send_telegram(token: str, chat_id: int, message: str) -> None:
    """Send a Telegram message.  No-op when *token* is empty or *chat_id* is 0."""
    if not token or chat_id == 0:
        return
    resp = httpx.post(
        f"https://api.telegram.org/bot{token}/sendMessage",
        json={"chat_id": chat_id, "text": message, "parse_mode": "HTML"},
    )
    resp.raise_for_status()


# ── browser helpers ──────────────────────────────────────────────────────────

def _build_proxy(server: str, username: str, password: str) -> Optional[dict]:
    """Build a Playwright-style proxy dict from environment variables."""
    if not server:
        return None
    proxy: dict = {"server": server}
    if username:
        proxy["username"] = username
    if password:
        proxy["password"] = password
    return proxy


# ── tracker ──────────────────────────────────────────────────────────────────

class Tracker:
    """Runtime state for the polling loop."""

    def __init__(self) -> None:
        self.notified_items: dict[str, None] = {}

        self.tg_token: str = os.getenv("TELEGRAM_BOT_TOKEN", "")
        self.tg_chat_id: int = int(os.getenv("TELEGRAM_CHAT_ID", "0") or "0")

        self.proxy: Optional[dict] = _build_proxy(
            os.getenv("PROXY_SERVER", ""),
            os.getenv("PROXY_USERNAME", ""),
            os.getenv("PROXY_PASSWORD", ""),
        )

        # Injectable notification functions — replaced in tests.
        self.email_send: Callable[[str], None] = send_email
        self.telegram_send: Callable[[str], None] = (
            lambda msg: send_telegram(self.tg_token, self.tg_chat_id, msg)
        )

        # Browser state (set by launch_browser)
        self._browser_cm: Any = None   # Camoufox context-manager
        self._browser: Any = None      # BrowserContext or Browser
        self._pw: Any = None           # Playwright instance (Patchright only)
        self._engine: str = ""

    # ── browser lifecycle ─────────────────────────────────────────────────

    def launch_browser(self) -> None:
        """Start an anti-detect browser.  Prefers Camoufox; falls back to Patchright."""
        try:
            from camoufox.sync_api import Camoufox  # type: ignore[import-untyped]

            self._browser_cm = Camoufox(headless=True, proxy=self.proxy)
            self._browser = self._browser_cm.__enter__()
            self._engine = "camoufox"
            log.info("[browser] launched Camoufox (anti-detect Firefox)")
        except ImportError:
            from patchright.sync_api import sync_playwright  # type: ignore[import-untyped]

            self._pw = sync_playwright().start()
            launch_opts: dict = {"headless": True}
            if self.proxy:
                launch_opts["proxy"] = self.proxy
            self._browser = self._pw.chromium.launch(**launch_opts)
            self._engine = "patchright"
            log.info("[browser] launched Patchright (patched Chromium)")

    def close_browser(self) -> None:
        try:
            if self._browser_cm is not None:
                self._browser_cm.__exit__(None, None, None)
            elif self._browser is not None:
                self._browser.close()  # type: ignore[union-attr]
                if self._pw is not None:
                    self._pw.stop()  # type: ignore[union-attr]
        except Exception:
            pass
        self._browser = None
        self._browser_cm = None
        self._pw = None

    # ── scraping ──────────────────────────────────────────────────────────

    def warm_page(self) -> Any:
        """Open hermes.com so DataDome issues a valid session cookie."""
        page = self._browser.new_page()  # type: ignore[union-attr]
        page.goto(HERMES_BASE, wait_until="domcontentloaded")
        time.sleep(4)
        return page

    def fetch_products(self, page: Any, api_url: str) -> list[Item]:
        """Run fetch() inside the browser to inherit its TLS fingerprint + cookies."""
        js = f"""async () => {{
            const resp = await fetch({json.dumps(api_url)}, {{
                method: 'GET',
                headers: {{
                    'Accept':          'application/json, text/plain, */*',
                    'Accept-Language': 'zh-TW,zh;q=0.9,en-US;q=0.8',
                    'x-hermes-locale': 'tw_zh',
                    'Referer':         'https://www.hermes.com/tw/zh/',
                }},
                credentials: 'include',
            }});
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            return await resp.text();
        }}"""
        result = page.evaluate(js)
        return parse_api_response(result)

    # ── notification ──────────────────────────────────────────────────────

    def notify(self, items: list[Item]) -> None:
        for item in items:
            if item.sku in self.notified_items:
                continue

            try:
                self.telegram_send(build_telegram_message(item))
                if self.tg_token and self.tg_chat_id:
                    log.info("[telegram] sent: %s", item.slug)
            except Exception as exc:
                log.error("[telegram] error sending %s: %s", item.slug, exc)

            try:
                self.email_send(build_email_content(item))
                if os.getenv("SENDGRID_API_KEY"):
                    log.info("[email] sent: %s", item.slug)
            except Exception as exc:
                log.error("[email] error sending %s: %s", item.slug, exc)

            self.notified_items[item.sku] = None

    # ── scan loop ─────────────────────────────────────────────────────────

    def scan(self) -> None:
        log.info("[scan] starting...")
        page = self.warm_page()
        try:
            matched: list[Item] = []
            for api_url in API_URLS:
                try:
                    items = self.fetch_products(page, api_url)
                    matched.extend(filter_bags(items))
                except Exception as exc:
                    log.error("[scan] fetch error (%s): %s", api_url, exc)

            if not matched:
                log.info("[scan] 還沒發現任何包款")
                return

            for item in matched:
                log.info(
                    "[found] %s | %s | NT$%d",
                    item.title, item.avg_color, item.price,
                )
            self.notify(matched)
        finally:
            page.close()

    def run(self) -> None:
        self.launch_browser()
        log.info("[main] tracker started — polling every 10 minutes")
        try:
            while True:
                try:
                    self.scan()
                except Exception as exc:
                    log.error(
                        "[main] scan failed: %s — restarting browser", exc,
                    )
                    self.close_browser()
                    self.launch_browser()
                time.sleep(POLL_INTERVAL)
        finally:
            self.close_browser()


# ── entry point ──────────────────────────────────────────────────────────────

def main() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    load_dotenv()

    tracker = Tracker()
    tracker.run()


if __name__ == "__main__":
    main()
