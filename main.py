#!/usr/bin/env python3
"""Hermès product tracker — Camoufox / Patchright with residential-proxy support."""

import json
import logging
import os
import re
import time
from dataclasses import dataclass
from datetime import datetime, timezone, timedelta
from html import unescape
from typing import Any, Callable, Optional

from dotenv import load_dotenv
import httpx

log = logging.getLogger(__name__)

# ── constants ────────────────────────────────────────────────────────────────

HERMES_BASE = "https://www.hermes.com/tw/zh/"

BAG_PATTERN = re.compile(r"(?i).*(constance|lindy|kelly|picotin|bolide).*")

# Category pages to scrape for available products.
CATEGORY_URLS = [
    "https://www.hermes.com/tw/zh/category/leather-goods/bags-and-clutches/womens-bags-and-clutches/",
]

POLL_INTERVAL = 90  # seconds — daytime default (overridden at runtime by poll_interval())

# Taiwan time-based polling: faster during the day, slower at night.
_TW = timezone(timedelta(hours=8))
POLL_DAYTIME  = 45    # 07:00–21:00 Taiwan time
POLL_NIGHTTIME = None  # 21:00–07:00 Taiwan time — no scanning

def poll_interval() -> Optional[int]:
    """Return poll interval in seconds, or None if outside active hours."""
    hour = datetime.now(_TW).hour
    return POLL_DAYTIME if 7 <= hour < 21 else None

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


def parse_html_products(html: str) -> list[Item]:
    """Extract product items from Hermès category page HTML (Angular SSR)."""
    items: list[Item] = []
    for card in re.finditer(r'<h-grid-result-item[^>]*>(.*?)</h-grid-result-item>', html, re.DOTALL):
        block = card.group(1)

        # SKU from id="product-item-meta-H{SKU}"
        m_sku = re.search(r'id="product-item-meta-H([^"]+)"', block)
        if not m_sku:
            continue
        sku = m_sku.group(1)

        # Title from <span class="product-title">…</span>
        m_title = re.search(r'class="product-title"[^>]*>([^<]+)<', block)
        title = unescape(m_title.group(1).strip()) if m_title else ""

        # Color and href from <a … title="Title, Color" href="…">
        m_link = re.search(r'class="product-item-name"[^>]+href="([^"]+)"[^>]+title="([^"]+)"', block)
        url = m_link.group(1) if m_link else ""
        color = ""
        if m_link:
            parts = unescape(m_link.group(2)).split(", ", 1)
            color = parts[1] if len(parts) > 1 else ""

        # Price from <span class="price …"> NT$ xx,xxx </span>
        m_price = re.search(r'NT\$\s*([\d,]+)', block)
        price = int(m_price.group(1).replace(",", "")) if m_price else 0

        slug = url.strip("/").split("/")[-1] if url else ""
        items.append(Item(sku=sku, title=title, avg_color=color, price=price, url=url, slug=slug))
    return items


def filter_bags(items: list[Item]) -> list[Item]:
    """Return only items whose slug or title matches the target bag pattern."""
    return [
        item for item in items
        if BAG_PATTERN.match(item.slug) or BAG_PATTERN.match(item.title)
    ]


def item_product_url(item: Item) -> str:
    """Return the full product URL for an item."""
    url = item.url
    if url.startswith("http"):
        return url
    return "https://www.hermes.com" + ("" if url.startswith("/") else "/") + url


def build_telegram_message(item: Item) -> str:
    """Return HTML-formatted Telegram notification for one item."""
    return (
        f"🛍 <b>Hermès 進貨通知</b>\n\n"
        f"<b>{item.title}</b>\n"
        f"顏色：{item.avg_color}\n"
        f"售價：NT${item.price:,}\n"
        f'<a href="{item_product_url(item)}">查看商品 →</a>\n\n'
        f"@CharissaH 快看！"
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
        self._pages: dict[str, Any] = {}  # persistent pages per category URL

    # ── browser lifecycle ─────────────────────────────────────────────────

    def launch_browser(self) -> None:
        """Start an anti-detect browser.  Prefers Camoufox; falls back to Patchright."""
        try:
            from camoufox.sync_api import Camoufox  # type: ignore[import-untyped]

            self._browser_cm = Camoufox(headless=True, proxy=self.proxy, geoip=True)
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
        self._pages = {}

    # ── scraping ──────────────────────────────────────────────────────────

    def fetch_products_from_page(self, category_url: str) -> list[Item]:
        """Navigate/reload a persistent page and parse product items from the SSR HTML."""
        if category_url not in self._pages:
            page = self._browser.new_page()  # type: ignore[union-attr]
            self._pages[category_url] = page
        else:
            page = self._pages[category_url]
        try:
            page.goto(category_url, wait_until="domcontentloaded", timeout=60000)
            time.sleep(8)
            html = page.content()
        except Exception:
            # Page may have crashed; discard it so next call creates a fresh one
            try:
                page.close()
            except Exception:
                pass
            self._pages.pop(category_url, None)
            raise
        items = parse_html_products(html)
        log.info("[scrape] %s → %d products", category_url, len(items))
        return items

    # ── notification ──────────────────────────────────────────────────────

    def notify(self, items: list[Item]) -> None:
        for item in items:
            dedup_key = f"{item.sku}_{item.avg_color}"
            if dedup_key in self.notified_items:
                continue

            try:
                self.telegram_send(build_telegram_message(item))
                if self.tg_token and self.tg_chat_id:
                    log.info("[telegram] sent: %s (%s)", item.slug, item.avg_color)
            except Exception as exc:
                log.error("[telegram] error sending %s: %s", item.slug, exc)

            try:
                self.email_send(build_email_content(item))
                if os.getenv("SENDGRID_API_KEY"):
                    log.info("[email] sent: %s", item.slug)
            except Exception as exc:
                log.error("[email] error sending %s: %s", item.slug, exc)

            self.notified_items[dedup_key] = None

    # ── scan loop ─────────────────────────────────────────────────────────

    def scan(self) -> None:
        log.info("[scan] starting...")
        matched: list[Item] = []
        for category_url in CATEGORY_URLS:
            try:
                items = self.fetch_products_from_page(category_url)
                matched.extend(filter_bags(items))
            except Exception as exc:
                log.error("[scan] fetch error (%s): %s", category_url, exc)

        if not matched:
            log.info("[scan] 還沒發現任何包款")
            return

        for item in matched:
            log.info(
                "[found] %s | %s | NT$%d",
                item.title, item.avg_color, item.price,
            )
        self.notify(matched)

    def run(self) -> None:
        self.launch_browser()
        log.info("[main] tracker started — active 07:00–21:00 Taiwan time, every %ds", POLL_DAYTIME)
        try:
            while True:
                interval = poll_interval()
                if interval is None:
                    # Outside active hours — calculate seconds until 07:00 TW
                    now = datetime.now(_TW)
                    next_start = now.replace(hour=7, minute=0, second=0, microsecond=0)
                    if now.hour >= 21:
                        next_start = next_start + timedelta(days=1)
                    sleep_secs = int((next_start - now).total_seconds())
                    log.info("[main] outside active hours — sleeping %dm until 07:00", sleep_secs // 60)
                    time.sleep(sleep_secs)
                    continue
                try:
                    self.scan()
                except Exception as exc:
                    log.error(
                        "[main] scan failed: %s — restarting browser", exc,
                    )
                    self.close_browser()
                    self.launch_browser()
                log.info("[main] next scan in %d seconds", interval)
                time.sleep(interval)
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
