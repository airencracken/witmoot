// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkInvitations(browser, base, storageState, { path = '/invites', screenshots, app }) {
  // Copy only the authenticated cookies: each context starts with system colors.
  for (const javaScriptEnabled of [true, false]) {
    const context = await browser.newContext({ javaScriptEnabled, storageState: { cookies: storageState.cookies, origins: [] } });
    const page = await context.newPage();
    try {
      await page.goto(base + path);
      assert.equal(await page.locator('h1').innerText(), 'Invitations');
      assert.equal(await page.locator('.eyebrow, .invitation-page table').count(), 0);
      const label = `Invitation for ${javaScriptEnabled ? 'script' : 'plain'} `.padEnd(64, 'w');
      await page.getByLabel('Label (optional)', { exact: true }).fill(label);
      await page.getByLabel('Uses', { exact: true }).fill('2');
      await page.getByLabel('Expires after (days)', { exact: true }).fill('7');
      await page.getByRole('button', { name: 'Create invitation', exact: true }).click();
      const link = await page.getByLabel('Invitation link', { exact: true }).inputValue();
      assert.equal(new URL(link).origin, base);
      assert.equal(await page.locator('.invitation-result').count(), 1);
      const guestContext = await browser.newContext({ javaScriptEnabled });
      try {
        const guest = await guestContext.newPage();
        await guest.goto(link);
        assert.equal(await guest.locator('h1').innerText(), 'Create an account');
        for (const width of [320, 1280]) {
          await guest.setViewportSize({ width, height: 960 });
          assert.ok(await guest.evaluate(() => {
            const form = document.querySelector('.account-page').getBoundingClientRect();
            return document.documentElement.scrollWidth <= innerWidth && form.width <= 480 && form.left >= 0 && form.right <= innerWidth;
          }), `${app}: invitation acceptance is compact at ${width}px`);
          if (screenshots && javaScriptEnabled) {
            await mkdir(screenshots, { recursive: true });
            await guest.screenshot({ path: join(screenshots, `join-${width}.png`), fullPage: true });
          }
        }
      } finally { await guestContext.close(); }

      await page.goto(base + path);
      assert.equal(await page.locator('.invitation-result').count(), 0, 'Invitation secret disappears on a fresh visit');
      for (const theme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: theme });
        for (const width of [320, 390, 768, 1280]) {
          await page.setViewportSize({ width, height: 960 });
          const layout = await page.evaluate(() => {
            const form = document.querySelector('.invitation-create').getBoundingClientRect();
            const list = document.querySelector('.invitation-list').getBoundingClientRect();
            return {
              fits: document.documentElement.scrollWidth <= innerWidth,
              beside: form.right < list.left && Math.abs(form.top - list.top) < 1,
              stacked: form.bottom < list.top,
              readableStatuses: [...document.querySelectorAll('.invitation-status')].every(status => {
                const luminance = color => {
                  const channels = color.match(/[\d.]+/g).slice(0, 3).map(Number).map(value => {
                    value /= 255;
                    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4;
                  });
                  return .2126 * channels[0] + .7152 * channels[1] + .0722 * channels[2];
                };
                const style = getComputedStyle(status);
                const first = luminance(style.color), second = luminance(style.backgroundColor);
                return (Math.max(first, second) + .05) / (Math.min(first, second) + .05) >= 4.5;
              }),
              fields: [...document.querySelectorAll('.invitation-create input:not([type="hidden"])')].every(input => {
                const bounds = input.getBoundingClientRect();
                return bounds.width >= 90 && bounds.height >= 44 && bounds.left >= 0 && bounds.right <= innerWidth;
              }),
            };
          });
          assert.ok(layout.readableStatuses, `${app}: readable invitation statuses in ${theme}`);
          assert.ok(layout.fits && layout.fields, `${app}: controls fit at ${width}px (${theme}, JavaScript ${javaScriptEnabled})`);
          assert.ok(width > 900 ? layout.beside : layout.stacked, `${app}: responsive form/list layout at ${width}px`);
          if (screenshots && javaScriptEnabled && [390, 1280].includes(width)) {
            await mkdir(screenshots, { recursive: true });
            await page.screenshot({ path: join(screenshots, `invitations-${theme}-${width}.png`), fullPage: true });
          }
        }
      }
      await page.getByLabel('Label (optional)', { exact: true }).focus();
      await page.keyboard.press('Tab');
      assert.equal(await page.locator('#invite-uses').evaluate(element => element === document.activeElement), true, 'Uses follows Label in keyboard order');
      const record = page.locator('.invitation-record').filter({ has: page.getByRole('heading', { name: label, exact: true }) });
      await record.getByRole('button', { name: `Revoke invitation ${label}`, exact: true }).click();
      await page.waitForURL(base + path + (app === 'witmoot' ? '?saved=1' : ''));
      await record.locator('.invitation-status').filter({ hasText: /revoked/i }).waitFor();
      assert.match(await record.innerText(), /revoked/i);
    } finally { await context.close(); }
  }
  console.log(`PASS: ${app} invitation layouts at four widths in both themes, with and without JavaScript; create, one-time link, keyboard order, and revoke`);
}
