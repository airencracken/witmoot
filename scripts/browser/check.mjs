import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkThemes } from './themes.mjs';
import { checkBoards } from './boards.mjs';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../../', import.meta.url));
const binary = resolve(root, 'bin/witmoot');
const data = await mkdtemp(join(tmpdir(), 'witmoot-browser-'));
const socket = createServer();
await new Promise((done, reject) => { socket.once('error', reject); socket.listen(0, '127.0.0.1', done); });
const port = socket.address().port;
await new Promise(done => socket.close(done));
const origin = `http://127.0.0.1:${port}`;
const env = { ...process.env, WITMOOT_DATA_DIR: data, WITMOOT_ADDR: `127.0.0.1:${port}`, WITMOOT_BASE_URL: origin, WITMOOT_TRUSTED_PROXIES: '', WITMOOT_SECURE_COOKIES: 'false', WITMOOT_IMVAULT_URL: '' };
let server;
let browser;
const password = 'a friendly browser test password';
const problems = [];

async function ready() {
	for (let tries = 0; tries < 100; tries++) {
		if (server.exitCode !== null) throw new Error(`Server exited: ${server.exitCode}`);
		try { if ((await fetch(origin + '/healthz')).ok) return; } catch { /* Server is starting. */ }
		await new Promise(done => setTimeout(done, 100));
	}
	throw new Error('Server did not become ready');
}

async function flow(javaScriptEnabled) {
	const context = await browser.newContext({ javaScriptEnabled, colorScheme: javaScriptEnabled ? 'dark' : 'light', viewport: { width: 1280, height: 900 } });
	const page = await context.newPage();
	page.on('pageerror', error => problems.push(error.message));
	page.on('console', message => { if (message.type() === 'error' && /Content Security Policy|Refused to/.test(message.text())) problems.push(message.text()); });
	await page.goto(origin);
	assert.match(await page.locator('h1').innerText(), /Good company/);
	await page.getByRole('link', { name: 'Come on in', exact: false }).first().click();
	await page.getByLabel('Username', { exact: true }).fill('alex');
	await page.getByLabel('Password', { exact: true }).fill(password);
	await page.getByRole('button', { name: 'Come on in' }).click();
	await page.waitForURL(origin + '/');
	assert.match(await page.locator('h1').innerText(), /Good to see you, alex/);
	await page.getByRole('link', { name: 'The kitchen table', exact: true }).click();
	await page.getByRole('link', { name: 'Start a conversation', exact: true }).click();
	const title = javaScriptEnabled ? 'Sunday lunch together' : 'An afternoon in the garden';
	await page.getByLabel('Give it a title').fill(title);
	await page.getByLabel('Your message').fill('Shall we get together this weekend?\nI can bring soup.');
	await page.getByRole('button', { name: 'Start conversation', exact: true }).click();
	await page.waitForURL(/\/topics\/\d+$/);
	const topicURL = page.url();
	assert.equal(await page.locator('h1').innerText(), title);
	await page.getByLabel('Your reply').fill('That sounds lovely. I will bring bread.');
	await page.getByRole('button', { name: 'Post reply' }).click();
	await page.waitForURL(/#post-/);
	assert.equal(await page.locator('.post').count(), 2);
	assert.match(await page.locator('.post-body').last().innerText(), /bring bread/);
	if (javaScriptEnabled) {
		// Trigger server-side validation and verify HTMX displays the error and keeps the form usable.
		await page.getByLabel('Your reply').fill('   ');
		await page.getByRole('button', { name: 'Post reply' }).click();
		await page.getByRole('alert').waitFor();
		assert.match(await page.getByRole('alert').innerText(), /Write a reply/);
		assert.equal(await page.getByRole('alert').evaluate(el => el === document.activeElement), true);
		await page.goto(topicURL);
	}
	await page.getByRole('link', { name: 'Search', exact: true }).click();
	await page.getByLabel('Search conversations and messages').fill(title);
	await page.getByRole('button', { name: 'Search', exact: true }).click();
	await page.getByRole('link', { name: title, exact: true }).waitFor();
	await page.getByRole('link', { name: 'Invites', exact: true }).click();
	const inviteLabel = javaScriptEnabled ? 'Sunday guests' : 'Garden guests';
	await page.getByLabel('Label (optional)', { exact: true }).fill(inviteLabel);
	await page.getByLabel('Uses', { exact: true }).fill('2');
	await page.getByLabel('Expires after (days)', { exact: true }).fill('14');
	await page.getByRole('button', { name: 'Create an invitation' }).click();
	const invite = await page.getByRole('link', { name: 'Invitation link', exact: true }).getAttribute('href');
	assert.equal(new URL(invite).origin, origin);
	assert.match(new URL(invite).search, /^\?invite=[a-f0-9]{64}$/);
	assert.equal(await page.getByLabel('Your invitation link').inputValue(), invite);
	const guest = await browser.newContext({ javaScriptEnabled });
	const guestPage = await guest.newPage();
	await guestPage.goto(invite);
	await guestPage.getByLabel('Username', { exact: true }).fill(javaScriptEnabled ? 'jules' : 'sam');
	await guestPage.getByLabel('Password', { exact: true }).fill(password);
	await guestPage.getByRole('button', { name: 'Join our board' }).click();
	await guestPage.waitForURL(origin + '/');
	assert.equal(await guestPage.getByRole('link', { name: 'Invites', exact: true }).count(), 0);
	await guest.close();
	await page.goto(origin + '/invites');
	const invitation = page.locator('.invitation-record').filter({ has: page.getByRole('heading', { name: inviteLabel, exact: true }) });
	assert.match(await invitation.innerText(), /1 of 2 used/);
	await invitation.getByRole('button', { name: 'Revoke invitation', exact: true }).click();
	await page.waitForURL(origin + '/invites?saved=1');
	assert.match(await invitation.innerText(), /revoked/);
	const lateGuest = await browser.newContext({ javaScriptEnabled });
	const latePage = await lateGuest.newPage();
	await latePage.goto(invite);
	await latePage.getByLabel('Username', { exact: true }).fill(javaScriptEnabled ? 'latejules' : 'latesam');
	await latePage.getByLabel('Password', { exact: true }).fill(password);
	await latePage.getByRole('button', { name: 'Join our board' }).click();
	await latePage.getByRole('alert').waitFor();
	assert.match(await latePage.getByRole('alert').innerText(), /revoked/);
	await lateGuest.close();
	await page.getByRole('link', { name: 'Our board', exact: true }).first().click();
	await page.getByRole('heading', { name: 'Good to see you, alex.', exact: true }).waitFor();
	if (javaScriptEnabled) {
		const screenshotDir = process.env.WITMOOT_SCREENSHOT_DIR;
		if (screenshotDir) {
			await mkdir(screenshotDir, { recursive: true });
			await page.screenshot({ path: join(screenshotDir, 'board-desktop.png'), fullPage: true });
			await page.getByRole('combobox', { name: 'Color theme' }).selectOption('light');
			await page.screenshot({ path: join(screenshotDir, 'board-light.png'), fullPage: true });
			await page.getByRole('combobox', { name: 'Color theme' }).selectOption('dark');
		}
		await page.setViewportSize({ width: 390, height: 844 });
		assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Mobile page overflows');
		if (screenshotDir) await page.screenshot({ path: join(screenshotDir, 'board-mobile.png'), fullPage: true });
	}
	await page.getByRole('button', { name: 'Sign out' }).click();
	await page.waitForURL(origin + '/');
	await page.getByRole('heading', { name: /Good company/ }).waitFor();
	await page.goto(topicURL);
	await page.waitForURL(origin + '/login');
	await context.close();
	console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} sign-in, topics, replies, search, invitations, private access, sign-out`);
}


async function modeFlow(javaScriptEnabled) {
	const context = await browser.newContext({ javaScriptEnabled, colorScheme: javaScriptEnabled ? 'dark' : 'light', viewport: { width: 1280, height: 900 } });
	const page = await context.newPage();
	page.on('pageerror', error => problems.push(error.message));
	page.on('console', message => { if (message.type() === 'error' && /Content Security Policy|Refused to/.test(message.text())) problems.push(message.text()); });
	await page.goto(origin + '/login');
	await page.getByLabel('Username', { exact: true }).fill('alex');
	await page.getByLabel('Password', { exact: true }).fill(password);
	await page.getByRole('button', { name: 'Come on in' }).click();
	await page.waitForURL(origin + '/');
	const guestContext = await browser.newContext({ javaScriptEnabled });
	const guest = await guestContext.newPage();
	async function choose(mode) {
		await page.goto(origin + '/settings');
		await page.getByRole('radio', { name: new RegExp(`^${mode}`) }).check();
		await page.getByRole('button', { name: 'Save settings' }).click();
		await page.waitForURL(origin + '/settings?saved=1');
		assert.equal(await page.getByRole('radio', { name: new RegExp(`^${mode}`) }).isChecked(), true);
		assert.match(await page.getByRole('status').innerText(), /settings are saved/);
	}
	await choose('Open');
	await guest.goto(origin);
	assert.match(await guest.locator('h1').innerText(), /Welcome to Witmoot/);
	assert.equal(await guest.getByRole('link', { name: 'Sunday lunch together', exact: true }).count(), 0);
	await page.goto(origin + '/boards/1/new');
	assert.match(await page.locator('.audience-note').innerText(), /Public/);
	const title = javaScriptEnabled ? 'An open picnic' : 'A public garden day';
	await page.getByLabel('Give it a title').fill(title);
	await page.getByLabel('Your message').fill('Come along and bring a friend.');
	await page.getByRole('button', { name: 'Start conversation', exact: true }).click();
	await page.waitForURL(/\/topics\/\d+$/);
	const publicURL = page.url();
	await guest.goto(publicURL);
	assert.equal(await guest.locator('h1').innerText(), title);
	assert.equal(await guest.getByRole('button', { name: 'Post reply' }).count(), 0);
	await guest.goto(origin + '/search?q=' + encodeURIComponent(title));
	await guest.getByRole('link', { name: title, exact: true }).waitFor();
	await guest.goto(origin + '/join');
	await guest.getByLabel('Username', { exact: true }).fill(javaScriptEnabled ? 'robin' : 'taylor');
	await guest.getByLabel('Password', { exact: true }).fill(password);
	await guest.getByRole('button', { name: 'Join our board' }).click();
	await guest.waitForURL(origin + '/');
	assert.equal(await guest.getByRole('link', { name: 'Settings', exact: true }).count(), 0);
	await guest.goto(publicURL);
	await guest.getByLabel('Your reply').fill('Count me in.');
	await guest.getByRole('button', { name: 'Post reply' }).click();
	await guest.waitForURL(/#post-/);
	assert.match(await guest.locator('.post-body').last().innerText(), /Count me in/);
	await choose('Personal');
	const response = await guest.goto(publicURL);
	assert.equal(response.status(), 403);
	assert.equal(await guest.locator('.post-body').count(), 0);
	assert.equal(await page.getByRole('link', { name: 'Invites', exact: true }).count(), 0);
	await page.goto(origin + '/boards/1/new');
	assert.match(await page.locator('.audience-note').innerText(), /Owners only/);
	if (javaScriptEnabled) {
		await page.goto(origin + '/settings');
		if (process.env.WITMOOT_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, 'settings-desktop.png'), fullPage: true });
		await page.setViewportSize({ width: 390, height: 844 });
		assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'Settings overflows on mobile');
		if (process.env.WITMOOT_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, 'settings-mobile.png'), fullPage: true });
	}
	await choose('Private');
	await guest.goto(publicURL);
	assert.equal(await guest.locator('h1').innerText(), title); // Existing account access is restored.
	await guest.getByRole('button', { name: 'Sign out' }).click();
	await guest.waitForURL(origin + '/');
	await guest.getByRole('heading', { name: /Good company/ }).waitFor();
	await guest.goto(publicURL);
	await guest.waitForURL(origin + '/login');
	await guestContext.close();
	await context.close();
	console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} settings, all modes, audience labels, public browsing, open signup, and restored access`);
}
try {
	execFileSync(binary, ['create-owner', '--username', 'alex', '--password-stdin'], { env, input: password + '\n' });
	server = spawn(binary, [], { env, stdio: ['ignore', 'ignore', 'pipe'] });
	server.stderr.on('data', chunk => process.stderr.write(chunk));
	await ready();
	browser = await chromium.launch({ headless: true });
	await checkThemes(browser, origin);
	await flow(true);
	await flow(false);
	await modeFlow(true);
	await modeFlow(false);
	await checkBoards(browser, origin, password, true);
	await checkBoards(browser, origin, password, false);
	assert.deepEqual(problems, [], 'Browser script or CSP errors');
} finally {
	if (browser) await browser.close();
	if (server && server.exitCode === null) {
		const exited = new Promise(done => server.once('exit', done));
		server.kill('SIGTERM');
		await exited;
	}
	await rm(data, { recursive: true, force: true });
}
