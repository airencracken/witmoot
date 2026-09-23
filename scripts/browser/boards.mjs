import assert from 'node:assert/strict';
import { join } from 'node:path';
import { checkLifecycle } from './lifecycle.mjs';

export async function checkBoards(browser, origin, password, javaScriptEnabled) {
	const contexts = [];
	const errors = [];
	async function signIn(username) {
		const context = await browser.newContext({ javaScriptEnabled, colorScheme: 'dark', viewport: { width: 1280, height: 900 } });
		contexts.push(context);
		context.on('page', page => page.on('pageerror', error => errors.push(error.message)));
		const page = await context.newPage();
		await page.goto(origin + '/login');
		await page.getByLabel('Username', { exact: true }).fill(username);
		await page.getByLabel('Password', { exact: true }).fill(password);
		await page.getByRole('button', { name: 'Come on in' }).click();
		await page.waitForURL(origin + '/');
		return page;
	}
	try {
		const owner = await signIn('alex');
		await owner.goto(origin + '/settings');
		await owner.getByRole('radio', { name: /^Open/ }).check();
		await owner.getByRole('button', { name: 'Save settings' }).click();
		await owner.waitForURL(origin + '/settings?saved=1');
		await owner.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Manage boards', exact: true }).click();
		await owner.getByRole('link', { name: 'Create a board', exact: true }).click();
		const name = javaScriptEnabled ? 'Quiet plans with HTMX' : 'Quiet plans without scripts';
		await owner.getByLabel('Board name', { exact: true }).fill(name);
		await owner.getByLabel('Category', { exact: true }).fill('A smaller table');
		await owner.getByLabel('Description', { exact: true }).fill('A place for just a few people.');
		await owner.getByLabel('jules', { exact: true }).selectOption('write');
		await owner.getByLabel('sam', { exact: true }).selectOption('read');
		await owner.getByRole('button', { name: 'Create board', exact: true }).click();
		await owner.waitForURL(/\/boards\/\d+\/settings\?saved=1$/);
		const settingsURL = owner.url();
		const boardURL = settingsURL.replace('/settings?saved=1', '');
		assert.match(await owner.getByRole('status').innerText(), /settings are saved/);
		await owner.goto(boardURL);
		await owner.getByRole('link', { name: 'Start a conversation', exact: true }).click();
		assert.match(await owner.locator('.audience-note').innerText(), /Selected board members/);
		await owner.getByLabel('Give it a title').fill('A surprise for our friends');
		await owner.getByLabel('Your message').fill('This stays in our private room.');
		await owner.getByRole('button', { name: 'Start conversation', exact: true }).click();
		await owner.waitForURL(/\/topics\/\d+$/);
		const topicURL = owner.url();
		const writer = await signIn('jules');
		await writer.goto(topicURL);
		await writer.getByLabel('Your reply').fill('The picnic is on Saturday.');
		await writer.getByRole('button', { name: 'Post reply', exact: true }).click();
		await writer.waitForURL(/#post-/);
		assert.equal(await writer.getByRole('link', { name: 'Edit message 1', exact: true }).count(), 0);
		await writer.getByRole('link', { name: 'Edit message 2', exact: true }).click();
		await writer.waitForURL(/\/posts\/\d+\/edit$/);
		const editURL = writer.url();
		const stale = await writer.context().newPage();
		await stale.goto(editURL);
		await writer.getByLabel('Your message', { exact: true }).fill('The picnic is on Sunday.\nPlease bring soup.');
		await writer.getByRole('button', { name: 'Save changes', exact: true }).click();
		await writer.waitForURL(/#post-/);
		assert.match(await writer.locator('.post').last().innerText(), /Edited .*UTC/);
		assert.match(await writer.locator('.post-body').last().innerText(), /Sunday/);
		await stale.getByLabel('Your message', { exact: true }).fill('A stale change');
		await stale.getByRole('button', { name: 'Save changes', exact: true }).click();
		await stale.getByRole('alert').waitFor();
		assert.match(await stale.getByRole('alert').innerText(), /has changed since/);
		assert.equal(await stale.getByLabel('Your message', { exact: true }).inputValue(), 'A stale change');
		const reader = await signIn('sam');
		await reader.goto(topicURL);
		assert.match(await reader.locator('.audience-note').innerText(), /read-only/);
		assert.equal(await reader.getByRole('button', { name: 'Post reply', exact: true }).count(), 0);
		assert.equal(await reader.getByRole('link', { name: /^Edit message/ }).count(), 0);
		await reader.goto(boardURL);
		assert.equal(await reader.getByRole('link', { name: 'Start a conversation', exact: true }).count(), 0);
		const outsider = await signIn('robin');
		assert.equal(await outsider.getByRole('link', { name, exact: true }).count(), 0);
		assert.equal((await outsider.goto(boardURL)).status(), 404);
		assert.equal((await outsider.goto(topicURL)).status(), 404);
		const guestContext = await browser.newContext({ javaScriptEnabled });
		contexts.push(guestContext);
		const guest = await guestContext.newPage();
		assert.equal((await guest.goto(topicURL)).status(), 404);
		await guest.goto(origin);
		assert.equal(await guest.getByRole('link', { name, exact: true }).count(), 0);
		assert.match(await guest.locator('.site-access').innerText(), /Public browsing.*Open registration/);

		await writer.goto(editURL);
		await owner.goto(settingsURL);
		await owner.getByLabel('jules', { exact: true }).selectOption('none');
		await owner.getByRole('button', { name: 'Save board', exact: true }).click();
		await owner.getByRole('status').waitFor();
		await writer.getByLabel('Your message', { exact: true }).fill('Cannot save after revocation');
		await writer.getByRole('button', { name: 'Save changes', exact: true }).click();
		await writer.getByRole('heading', { name: 'Not Found', exact: true }).waitFor();
		await reader.goto(topicURL);
		assert.match(await reader.locator('.post-body').last().innerText(), /Sunday/);

		await owner.setViewportSize({ width: 390, height: 844 });
		assert.equal(await owner.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Board access form overflows');
		if (process.env.WITMOOT_SCREENSHOT_DIR) await owner.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, `board-access-${javaScriptEnabled ? 'dark' : 'nojs'}.png`), fullPage: true });
		await checkLifecycle({ owner, reader, outsider, guest, origin, boardURL, topicURL, name, javaScriptEnabled });
		assert.deepEqual(errors, []);
		console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'No JavaScript'} board management, read-only/hidden access, edited timestamps, conflicts, and revocation`);
	} finally {
		for (const context of contexts) await context.close();
	}
}
