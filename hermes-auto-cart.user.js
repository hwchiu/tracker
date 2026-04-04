// ==UserScript==
// @name         Hermès 自動加入購物車
// @namespace    https://github.com/hwchiu/tracker
// @version      1.0
// @description  開啟愛馬仕商品頁時自動點擊「加入購物車」
// @author       hwchiu
// @match        https://www.hermes.com/tw/zh/product/*
// @grant        none
// @run-at       document-idle
// ==/UserScript==

(function () {
    'use strict';

    const SELECTOR = 'button[name="add-to-cart"][aria-disabled="false"]';
    const MAX_WAIT_MS = 15000;
    const INTERVAL_MS = 500;

    let elapsed = 0;

    const timer = setInterval(() => {
        elapsed += INTERVAL_MS;

        const btn = document.querySelector(SELECTOR);
        if (btn) {
            clearInterval(timer);
            console.log('[Hermès Tracker] 找到加入購物車按鈕，自動點擊...');
            btn.click();
            return;
        }

        if (elapsed >= MAX_WAIT_MS) {
            clearInterval(timer);
            console.warn('[Hermès Tracker] 等待逾時，找不到加入購物車按鈕');
        }
    }, INTERVAL_MS);
})();
