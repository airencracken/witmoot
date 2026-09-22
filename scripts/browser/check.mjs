import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../../', import.meta.url));
const binary = resolve(root, 'bin/witmoot');
const data = await mkdtemp(join(tmpdir(), 'witmoot-browser-'));
const socket = createServer();
await new Promise((done, reject) => { socket.once('error', reject); socket.listen(0, '127.0.0.1', done); });
const port = socket.address().port;
await new Promise(done => socket.close(done));
const origin = `http://127.0.0.1:${port}`;
const env = { ...process.env, WITMOOT_DATA_DIR: data, WITMOOT_ADDR: `127.0.0.1:${port}`, WITMOOT_SECURE_COOKIES: 'false' };
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
	const context = await browser.newContext({ javaScriptEnabled, viewport: { width: 1280, height: 900 } });
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
	await page.getByRole('button', { name: 'Create an invitation' }).click();
	const invite = await page.getByRole('link', { name: 'Invitation link', exact: true }).getAttribute('href');
	assert.match(invite, /^\/join\?invite=[a-f0-9]{64}$/);
	if (javaScriptEnabled) {
		await page.waitForFunction(() => document.querySelector('[data-invitation]')?.value.startsWith(window.location.origin));
		assert.match(await page.getByLabel('Your invitation link').inputValue(), /^http:\/\/127\.0\.0\.1:/);
	}
	const guest = await browser.newContext({ javaScriptEnabled });
	const guestPage = await guest.newPage();
	await guestPage.goto(origin + invite);
	await guestPage.getByLabel('Username', { exact: true }).fill(javaScriptEnabled ? 'jules' : 'sam');
	await guestPage.getByLabel('Password', { exact: true }).fill(password);
	await guestPage.getByRole('button', { name: 'Join our board' }).click();
	await guestPage.waitForURL(origin + '/');
	assert.equal(await guestPage.getByRole('link', { name: 'Invites', exact: true }).count(), 0);
	await guest.close();
	await page.getByRole('link', { name: 'Our board', exact: true }).first().click();
	await page.getByRole('heading', { name: 'Good to see you, alex.', exact: true }).waitFor();
	if (javaScriptEnabled) {
		const screenshotDir = process.env.WITMOOT_SCREENSHOT_DIR;
		if (screenshotDir) {
			await mkdir(screenshotDir, { recursive: true });
			await page.screenshot({ path: join(screenshotDir, 'board-desktop.png'), fullPage: true });
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

try {
	execFileSync(binary, ['create-owner', '--username', 'alex', '--password-stdin'], { env, input: password + '\n' });
	server = spawn(binary, [], { env, stdio: ['ignore', 'ignore', 'pipe'] });
	server.stderr.on('data', chunk => process.stderr.write(chunk));
	await ready();
	browser = await chromium.launch({ headless: true });
	await flow(true);
	await flow(false);
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
