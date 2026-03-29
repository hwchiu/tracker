"""Tests for the Hermès tracker — mirrors the Go test suite in main_test.go."""

import json
import os
import threading
from http.server import HTTPServer, BaseHTTPRequestHandler

import pytest

import main as m


# ── helpers ───────────────────────────────────────────────────────────────────

SAMPLE_ITEMS = [
    m.Item(sku="K25", title="Kelly 25 Sellier", slug="kelly-25-sellier", avg_color="Noir", price=230000, url="product/kelly-25"),
    m.Item(sku="K35", title="Kelly 35", slug="kelly-35", avg_color="Gold", price=260000, url="product/kelly-35"),
    m.Item(sku="B30", title="Birkin 30", slug="birkin-30", avg_color="Etoupe", price=310000, url="product/birkin-30"),
    m.Item(sku="L26", title="Lindy 26", slug="lindy-26", avg_color="Rose Sakura", price=180000, url="product/lindy-26"),
    m.Item(sku="C18", title="Constance 18 Mini", slug="constance-18-mini", avg_color="Gold", price=200000, url="product/constance-18"),
    m.Item(sku="P18", title="Picotin 18", slug="picotin-18", avg_color="Bleu de Malte", price=90000, url="product/picotin-18"),
    m.Item(sku="GP36", title="Garden Party 36", slug="garden-party-36", avg_color="Vert", price=120000, url="product/garden-party-36"),
    m.Item(sku="BO31", title="Bolide 31", slug="bolide-31", avg_color="Rouge H", price=170000, url="product/bolide-31"),
]


def noop_tracker() -> m.Tracker:
    """Return a Tracker with no-op email/telegram senders."""
    os.environ.pop("TELEGRAM_BOT_TOKEN", None)
    os.environ.pop("TELEGRAM_CHAT_ID", None)
    t = m.Tracker()
    t.email_send = lambda _content: None
    t.telegram_send = lambda _msg: None
    return t


# ── mock HTTP server (matches Go's httptest.NewServer) ────────────────────────

@pytest.fixture()
def mock_server():
    """Spin up a local HTTP server and yield its base URL + captured requests."""
    requests_received: list[dict] = []
    response_code = [202]
    response_body = [b""]

    class _Handler(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length) if length else b""
            requests_received.append({
                "method": "POST",
                "headers": dict(self.headers),
                "body": body.decode(),
            })
            self.send_response(response_code[0])
            self.end_headers()
            self.wfile.write(response_body[0])

        def log_message(self, *_args):
            pass  # suppress server logs during tests

    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    thread = threading.Thread(target=srv.serve_forever, daemon=True)
    thread.start()

    yield {
        "url": f"http://127.0.0.1:{srv.server_address[1]}",
        "requests": requests_received,
        "set_code": lambda c: response_code.__setitem__(0, c),
        "set_body": lambda b: response_body.__setitem__(0, b),
    }
    srv.shutdown()


# ── BAG_PATTERN ───────────────────────────────────────────────────────────────

class TestBagPattern:
    @pytest.mark.parametrize(
        "text, expected",
        [
            # should match
            ("constance-mini", True),
            ("constance-18", True),
            ("Constance Long Wallet", True),
            ("CONSTANCE", True),
            ("kelly-25", True),
            ("kelly-25-sellier", True),
            ("Kelly 35", True),
            ("KELLY", True),
            ("lindy-26", True),
            ("lindy-30", True),
            ("Lindy Mini", True),
            ("picotin-18", True),
            ("Picotin Lock 22", True),
            ("PICOTIN", True),
            # should NOT match
            ("birkin-30", False),
            ("birkin-40", False),
            ("bolide-31", False),
            ("garden-party-36", False),
            ("evelyne-29", False),
            ("plume", False),
            ("", False),
        ],
    )
    def test_bag_pattern_matches(self, text, expected):
        assert m.BAG_PATTERN.match(text) is not None if expected else m.BAG_PATTERN.match(text) is None


# ── parse_api_response ────────────────────────────────────────────────────────

class TestParseAPIResponse:
    def test_valid(self):
        raw = json.dumps({
            "total": 2,
            "products": {
                "items": [
                    {"sku": "H1", "title": "Kelly 25", "avgColor": "Noir", "price": 230000, "url": "product/kelly-25", "slug": "kelly-25"},
                    {"sku": "H2", "title": "Birkin 30", "avgColor": "Etoupe", "price": 310000, "url": "product/birkin-30", "slug": "birkin-30"},
                ]
            },
        })
        items = m.parse_api_response(raw)
        assert len(items) == 2
        k = items[0]
        assert k.sku == "H1"
        assert k.title == "Kelly 25"
        assert k.price == 230000
        assert k.avg_color == "Noir"
        assert k.slug == "kelly-25"

    def test_empty(self):
        items = m.parse_api_response('{"total":0,"products":{"items":[]}}')
        assert items == []

    def test_missing_fields(self):
        raw = '{"products":{"items":[{"sku":"X1","title":"Kelly Mini"}]}}'
        items = m.parse_api_response(raw)
        assert items[0].price == 0
        assert items[0].avg_color == ""

    def test_malformed(self):
        with pytest.raises(json.JSONDecodeError):
            m.parse_api_response("not json at all")

    def test_missing_products_key(self):
        items = m.parse_api_response('{"total":5}')
        assert items == []

    def test_large_payload(self):
        payload = {
            "products": {
                "items": [
                    {"sku": f"SKU{i:03d}", "title": f"Item {i}", "slug": f"item-{i}", "price": 100000 + i * 1000}
                    for i in range(40)
                ]
            }
        }
        items = m.parse_api_response(json.dumps(payload))
        assert len(items) == 40


# ── filter_bags ───────────────────────────────────────────────────────────────

class TestFilterBags:
    def test_returns_only_target_models(self):
        matched = m.filter_bags(SAMPLE_ITEMS)
        # kelly×2, lindy, constance, picotin = 5
        assert len(matched) == 5
        for item in matched:
            assert m.BAG_PATTERN.match(item.slug) or m.BAG_PATTERN.match(item.title)

    def test_excludes_non_target(self):
        non_target = [
            m.Item(sku="B1", title="Birkin 30", slug="birkin-30"),
            m.Item(sku="G1", title="Garden Party", slug="garden-party"),
            m.Item(sku="BO1", title="Bolide 31", slug="bolide-31"),
        ]
        assert m.filter_bags(non_target) == []

    def test_empty_input(self):
        assert m.filter_bags([]) == []

    def test_matches_by_title_when_slug_does_not(self):
        items = [m.Item(sku="X", title="Kelly Mini Compact Wallet", slug="compact-wallet")]
        assert len(m.filter_bags(items)) == 1

    def test_matches_by_slug_when_title_does_not(self):
        items = [m.Item(sku="X", title="Sac à main", slug="lindy-26")]
        assert len(m.filter_bags(items)) == 1

    def test_case_insensitive(self):
        items = [
            m.Item(sku="A", slug="KELLY-25"),
            m.Item(sku="B", slug="Constance-LONG"),
            m.Item(sku="C", slug="LINDY-26"),
            m.Item(sku="D", slug="PICOTIN-18"),
        ]
        assert len(m.filter_bags(items)) == 4


# ── item_product_url ──────────────────────────────────────────────────────────

class TestItemProductURL:
    def test_with_leading_slash(self):
        item = m.Item(url="/tw/zh/product/kelly-25")
        assert m.item_product_url(item) == m.HERMES_BASE + "tw/zh/product/kelly-25"

    def test_without_leading_slash(self):
        item = m.Item(url="product/lindy-26")
        assert m.item_product_url(item) == m.HERMES_BASE + "product/lindy-26"

    def test_empty(self):
        item = m.Item(url="")
        assert m.item_product_url(item) == m.HERMES_BASE


# ── build_telegram_message ────────────────────────────────────────────────────

class TestBuildTelegramMessage:
    def test_contains_fields(self):
        item = m.Item(sku="K25", title="Kelly 25", avg_color="Noir", price=230000, url="product/kelly-25")
        msg = m.build_telegram_message(item)
        for s in ("Kelly 25", "Noir", "230000", "product/kelly-25", "<b>", "<a href="):
            assert s in msg, f"message missing {s!r}"

    def test_html_format(self):
        item = m.Item(title="Lindy 26", avg_color="Rose", price=180000, url="product/lindy-26")
        msg = m.build_telegram_message(item)
        assert "<b>" in msg and "</b>" in msg

    def test_url_is_correct(self):
        item = m.Item(title="Constance 18", price=200000, url="/product/constance-18")
        msg = m.build_telegram_message(item)
        assert m.HERMES_BASE + "product/constance-18" in msg


# ── build_email_content ───────────────────────────────────────────────────────

class TestBuildEmailContent:
    def test_contains_fields(self):
        item = m.Item(title="Picotin 18", avg_color="Bleu de Malte", price=90000, url="product/picotin-18")
        content = m.build_email_content(item)
        for s in ("Picotin 18", "Bleu de Malte", "90000", "product/picotin-18"):
            assert s in content, f"email body missing {s!r}"

    def test_is_plain_text(self):
        item = m.Item(title="Kelly 35", avg_color="Gold", price=260000, url="product/kelly-35")
        content = m.build_email_content(item)
        assert "<b>" not in content and "<a " not in content


# ── Tracker.notify — deduplication ────────────────────────────────────────────

class TestNotify:
    def test_skips_already_notified_sku(self):
        tracker = noop_tracker()
        tracker.notified_items["K25"] = None
        email_calls = []
        tracker.email_send = lambda c: email_calls.append(c)
        tracker.notify([m.Item(sku="K25", title="Kelly 25")])
        assert len(email_calls) == 0

    def test_adds_new_sku_to_notified_set(self):
        tracker = noop_tracker()
        tracker.notify([m.Item(sku="L26", title="Lindy 26")])
        assert "L26" in tracker.notified_items

    def test_only_new_items_from_mixed_list(self):
        tracker = noop_tracker()
        tracker.notified_items["K25"] = None
        notified: list[str] = []
        tracker.email_send = lambda c: notified.append(c)
        tracker.notify([
            m.Item(sku="K25", title="Kelly 25"),    # already seen
            m.Item(sku="L26", title="Lindy 26"),    # new
            m.Item(sku="C18", title="Constance"),    # new
        ])
        assert len(notified) == 2

    def test_empty_list_does_nothing(self):
        tracker = noop_tracker()
        calls = []
        tracker.email_send = lambda c: calls.append(c)
        tracker.telegram_send = lambda c: calls.append(c)
        tracker.notify([])
        assert len(calls) == 0

    def test_calls_both_channels(self):
        tracker = noop_tracker()
        email_calls, tg_calls = [], []
        tracker.email_send = lambda c: email_calls.append(c)
        tracker.telegram_send = lambda c: tg_calls.append(c)
        tracker.notify([
            m.Item(sku="K25", title="Kelly 25"),
            m.Item(sku="L26", title="Lindy 26"),
        ])
        assert len(email_calls) == 2
        assert len(tg_calls) == 2

    def test_no_duplicate_on_second_run(self):
        tracker = noop_tracker()
        calls = []
        tracker.email_send = lambda c: calls.append(c)
        items = [m.Item(sku="K25", title="Kelly 25")]
        tracker.notify(items)
        tracker.notify(items)  # second scan, same item
        assert len(calls) == 1

    def test_email_error_does_not_stop_telegram(self):
        tracker = noop_tracker()
        tg_calls = []

        def _fail(_c):
            raise RuntimeError("sendgrid down")

        tracker.email_send = _fail
        tracker.telegram_send = lambda c: tg_calls.append(c)
        tracker.notify([m.Item(sku="K25", title="Kelly 25")])
        assert len(tg_calls) == 1
        assert "K25" in tracker.notified_items

    def test_telegram_error_does_not_stop_email(self):
        tracker = noop_tracker()
        email_calls = []

        def _fail(_c):
            raise RuntimeError("telegram down")

        tracker.telegram_send = _fail
        tracker.email_send = lambda c: email_calls.append(c)
        tracker.notify([m.Item(sku="L26", title="Lindy 26")])
        assert len(email_calls) == 1


# ── send_email ────────────────────────────────────────────────────────────────

class TestSendEmail:
    def test_skips_when_no_api_key(self, monkeypatch):
        monkeypatch.delenv("SENDGRID_API_KEY", raising=False)
        m.send_email("test content")  # should not raise

    def test_success_via_mock_server(self, mock_server, monkeypatch):
        orig = m.sendgrid_host
        m.sendgrid_host = mock_server["url"]
        try:
            monkeypatch.setenv("SENDGRID_API_KEY", "test-key")
            monkeypatch.setenv("SEND_FROM_NAME", "Tracker")
            monkeypatch.setenv("SEND_FROM_ADDRESS", "from@example.com")
            monkeypatch.setenv("SEND_TO_NAME", "Me")
            monkeypatch.setenv("SEND_TO_ADDRESS", "to@example.com")

            m.send_email("Kelly 25 is back!")

            req = mock_server["requests"][0]
            assert req["method"] == "POST"
            assert "Bearer " in req["headers"].get("authorization", req["headers"].get("Authorization", ""))
        finally:
            m.sendgrid_host = orig

    def test_returns_error_on_4xx(self, mock_server, monkeypatch):
        mock_server["set_code"](401)
        mock_server["set_body"](b'{"errors":[{"message":"invalid API key"}]}')

        orig = m.sendgrid_host
        m.sendgrid_host = mock_server["url"]
        try:
            monkeypatch.setenv("SENDGRID_API_KEY", "bad-key")
            monkeypatch.setenv("SEND_FROM_ADDRESS", "from@example.com")
            monkeypatch.setenv("SEND_TO_ADDRESS", "to@example.com")

            with pytest.raises(RuntimeError, match="401"):
                m.send_email("content")
        finally:
            m.sendgrid_host = orig

    def test_request_body_contains_content(self, mock_server, monkeypatch):
        orig = m.sendgrid_host
        m.sendgrid_host = mock_server["url"]
        try:
            monkeypatch.setenv("SENDGRID_API_KEY", "test-key")
            monkeypatch.setenv("SEND_FROM_ADDRESS", "from@example.com")
            monkeypatch.setenv("SEND_TO_ADDRESS", "to@example.com")

            content = "Kelly 25 available now"
            m.send_email(content)

            assert content in mock_server["requests"][0]["body"]
        finally:
            m.sendgrid_host = orig

    def test_includes_subject(self, mock_server, monkeypatch):
        orig = m.sendgrid_host
        m.sendgrid_host = mock_server["url"]
        try:
            monkeypatch.setenv("SENDGRID_API_KEY", "test-key")
            monkeypatch.setenv("SEND_FROM_ADDRESS", "from@example.com")
            monkeypatch.setenv("SEND_TO_ADDRESS", "to@example.com")

            m.send_email("content")

            assert "Hermes" in mock_server["requests"][0]["body"]
        finally:
            m.sendgrid_host = orig


# ── send_telegram ─────────────────────────────────────────────────────────────

class TestSendTelegram:
    def test_skips_when_token_is_empty(self):
        m.send_telegram("", 12345, "hello")  # should not raise

    def test_skips_when_chat_id_is_zero(self):
        m.send_telegram("some-token", 0, "hello")  # should not raise


# ── integration: parse → filter → notify ──────────────────────────────────────

class TestFullPipeline:
    def test_from_json_to_matched_items(self):
        payload = {
            "total": 6,
            "products": {
                "items": [
                    {"sku": "K25", "title": "Kelly 25", "slug": "kelly-25", "price": 230000, "avgColor": "Noir", "url": "product/kelly-25"},
                    {"sku": "B30", "title": "Birkin 30", "slug": "birkin-30", "price": 310000, "avgColor": "Etoupe", "url": "product/birkin-30"},
                    {"sku": "L26", "title": "Lindy 26", "slug": "lindy-26", "price": 180000, "avgColor": "Rose", "url": "product/lindy-26"},
                    {"sku": "C18", "title": "Constance 18", "slug": "constance-18", "price": 200000, "avgColor": "Gold", "url": "product/constance-18"},
                    {"sku": "P18", "title": "Picotin 18", "slug": "picotin-18", "price": 90000, "avgColor": "Blue", "url": "product/picotin-18"},
                    {"sku": "GP", "title": "Garden Party 36", "slug": "garden-party-36", "price": 120000, "avgColor": "Vert", "url": "product/garden-party"},
                ],
            },
        }
        items = m.parse_api_response(json.dumps(payload))
        assert len(items) == 6

        matched = m.filter_bags(items)
        assert len(matched) == 4  # kelly, lindy, constance, picotin

        skus = {item.sku for item in matched}
        for expected in ("K25", "L26", "C18", "P18"):
            assert expected in skus
        assert "B30" not in skus and "GP" not in skus

    def test_then_notify(self):
        raw = json.dumps({
            "products": {
                "items": [
                    {"sku": "K25", "title": "Kelly 25", "slug": "kelly-25", "price": 230000, "avgColor": "Noir", "url": "product/kelly-25"},
                    {"sku": "B30", "title": "Birkin 30", "slug": "birkin-30", "price": 310000, "avgColor": "Etoupe", "url": "product/birkin-30"},
                ],
            },
        })
        items = m.parse_api_response(raw)
        matched = m.filter_bags(items)

        tracker = noop_tracker()
        notified_skus: list[str] = []
        tracker.email_send = lambda c: notified_skus.append(c)
        tracker.notify(matched)

        assert len(notified_skus) == 1
        assert "Kelly 25" in notified_skus[0]
