import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import { createServer as httpServer, request as httpRequest } from 'node:http';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
assert.ok(process.env.IMVAULT_BINARY, 'Set IMVAULT_BINARY to an imvault executable to run the real API integration checks.');
const root = fileURLToPath(new URL('../../', import.meta.url));
const data = await mkdtemp(join(tmpdir(), 'witmoot-imvault-browser-'));
const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('IMVAULT_') && !key.startsWith('WITMOOT_')));
const children = [];
let browser;
let gateway;
let stallLibrary = false;
let libraryRequests = 0;
const password = 'a friendly integration test password';
const problems = [];
async function address() {
	const socket = createServer();
	await new Promise((done, reject) => { socket.once('error', reject); socket.listen(0, '127.0.0.1', done); });
	const addr = `127.0.0.1:${socket.address().port}`;
	await new Promise(done => socket.close(done));
	return addr;
}
async function start(binary, env, url) {
	const child = spawn(binary, [], { env, stdio: ['ignore', 'ignore', 'pipe'] });
	children.push(child);
	let logs = '';
	child.stderr.on('data', chunk => { logs += chunk; });
	for (let tries = 0; tries < 100; tries++) {
		if (child.exitCode !== null) throw new Error(`Server exited: ${logs}`);
		try { if ((await fetch(url)).ok) return; } catch { /* Starting. */ }
		await new Promise(done => setTimeout(done, 100));
	}
	throw new Error(`Server startup timed out: ${logs}`);
}
try {
	const vaultAddr = await address();
	const vaultBackendURL = `http://${vaultAddr}`;
	// Keep the real API behind a controllable gateway to exercise a slow
	// library without stopping the image server or relying on OS signals.
	gateway = httpServer((req, res) => {
		if (new URL(req.url, vaultBackendURL).pathname === '/api/v1/files') {
			libraryRequests++;
			if (stallLibrary) return;
		}
		const upstream = httpRequest(new URL(req.url, vaultBackendURL), { method: req.method, headers: req.headers }, response => {
			res.writeHead(response.statusCode, response.headers);
			response.pipe(res);
		});
		upstream.on('error', () => { res.writeHead(502); res.end(); });
		res.on('close', () => upstream.destroy());
		req.pipe(upstream);
	});
	await new Promise((done, reject) => { gateway.once('error', reject); gateway.listen(0, '127.0.0.1', done); });
	const vaultURL = `http://127.0.0.1:${gateway.address().port}`;
	const forumAddr = await address();
	const origin = `http://${forumAddr}`;
	const vaultEnv = { ...cleanEnv, IMVAULT_DATA_DIR: join(data, 'vault'), IMVAULT_ADDR: vaultAddr, IMVAULT_BASE_URL: vaultURL, IMVAULT_DEFAULT_VISIBILITY: 'public', IMVAULT_UPLOAD_BURST: '100' };
	const forumEnv = { ...cleanEnv, WITMOOT_DATA_DIR: join(data, 'forum'), WITMOOT_ADDR: forumAddr, WITMOOT_IMVAULT_URL: vaultURL };
	execFileSync(process.env.IMVAULT_BINARY, ['create-admin', '--username', 'alex', '--password-stdin'], { env: vaultEnv, input: password + '\n' });
	execFileSync(resolve(root, 'bin/witmoot'), ['create-owner', '--username', 'alex', '--password-stdin'], { env: forumEnv, input: password + '\n' });
	await start(process.env.IMVAULT_BINARY, vaultEnv, vaultURL + '/login');
	await start(resolve(root, 'bin/witmoot'), forumEnv, origin + '/healthz');
	browser = await chromium.launch({ headless: true });
	const vaultContext = await browser.newContext();
	const vaultPage = await vaultContext.newPage();
	await vaultPage.goto(vaultURL + '/login');
	await vaultPage.getByLabel('Username', { exact: true }).fill('alex');
	await vaultPage.getByLabel('Password', { exact: true }).fill(password);
	await vaultPage.getByRole('button', { name: 'Sign in', exact: true }).click();
	await vaultPage.waitForURL(vaultURL + '/gallery');
	await vaultPage.goto(vaultURL + '/settings/api-keys');
	await vaultPage.getByLabel('Label', { exact: true }).fill('Witmoot integration test');
	await vaultPage.getByRole('button', { name: 'Create key' }).click();
	await vaultPage.locator('.key-reveal').waitFor();
	const key = await vaultPage.locator('.key-reveal').inputValue();
	assert.ok(key.length > 20, 'imvault did not issue an API key');
	const auth = { Authorization: `Bearer ${key}` };
	// A real project PNG exercises multipart ingest and imvault's preview processing.
	const png = await readFile(join(root, 'internal/forum/static/moot-knight.png'));
	async function upload(name, visibility) {
		const form = new FormData();
		form.set('files', new Blob([png], { type: 'image/png' }), name);
		form.set('visibility', visibility);
		form.set('metadata', 'hidden');
		const response = await fetch(vaultURL + '/api/v1/upload', { method: 'POST', headers: auth, body: form });
		assert.equal(response.status, 201, 'Seed upload failed');
		return (await response.json()).files[0];
	}
	const publicImage = await upload('Public-table.png', 'public');
	const privateImage = await upload('Private-table.png', 'private');
	for (let i = 0; i < 11; i++) await upload(`Library-${i}.png`, 'private');
	for (const javaScriptEnabled of [true, false]) {
		const context = await browser.newContext({ javaScriptEnabled, viewport: { width: 1280, height: 960 } });
		const page = await context.newPage();
		page.on('pageerror', error => problems.push(error.message));
		page.on('console', message => { if (message.type() === 'error' && /Content Security Policy|Refused to/.test(message.text())) problems.push(message.text()); });
		await page.goto(origin + '/login');
		await page.getByLabel('Username', { exact: true }).fill('alex');
		await page.getByLabel('Password', { exact: true }).fill(password);
		await page.getByRole('button', { name: 'Come on in' }).click();
		await page.waitForURL(origin + '/');
		assert.equal(await page.locator('.mascot').evaluate(img => img.complete && img.naturalWidth > 0), true);
		await page.getByRole('link', { name: 'Images', exact: true }).click();
		await page.getByLabel('imvault API key', { exact: true }).fill(key);
		await page.getByRole('button', { name: 'Connect imvault', exact: true }).click();
		await page.waitForURL(origin + '/account/imvault?saved=1');
		assert.match(await page.getByRole('status').innerText(), /settings are saved/);
		assert.equal((await page.content()).includes(key), false, 'API key leaked in HTML');
		await page.getByRole('link', { name: 'Browse your library' }).click();
		await page.getByRole('heading', { name: 'Your imvault library', exact: true }).waitFor();
		await page.getByLabel('Search your library', { exact: true }).fill('Public-table');
		await page.getByRole('button', { name: 'Find images' }).click();
		await page.getByRole('link', { name: 'Public-table.png', exact: true }).waitFor();
		const beforeCompose = libraryRequests;
		await page.goto(origin + '/boards/1/new');
		assert.equal(libraryRequests, beforeCompose, 'Opening the composer fetched the library');
		const title = javaScriptEnabled ? 'Pictures with HTMX' : 'Pictures without JavaScript';
		await page.getByLabel('Give it a title').fill(title);
		await page.getByLabel('Your message', { exact: true }).fill('A place at the table for everyone.');
		await page.getByText('Add images', { exact: true }).click();
		await page.getByRole('button', { name: 'Load image library', exact: true }).click();
		await page.locator('.image-choice input[type=checkbox]').first().waitFor();
		assert.equal(await page.getByLabel('Give it a title').inputValue(), title, 'Loading images lost the title');
		assert.equal(await page.getByLabel('Your message', { exact: true }).inputValue(), 'A place at the table for everyone.', 'Loading images lost the draft');
		const firstID = await page.locator('.image-choice input[type=checkbox]').first().getAttribute('value');
		await page.locator('.image-choice input[type=checkbox]').first().check();
		if (javaScriptEnabled) {
			await page.getByRole('link', { name: 'Next images', exact: true }).click();
			await page.locator('.carried-images').waitFor();
			assert.equal(await page.locator(`input[name=image_ids][value="${firstID}"]`).isChecked(), true, 'Paging lost selection');
			await page.getByLabel('Find an image in your library').fill('Private-table');
			await page.locator(`.image-choice input[value="${privateImage.id}"]`).waitFor();
			await page.locator(`input[name=image_ids][value="${firstID}"]`).uncheck();
			await page.locator(`.image-choice input[value="${privateImage.id}"]`).check();
		}
		await page.getByLabel('Upload images', { exact: true }).setInputFiles({ name: 'New-table.png', mimeType: 'image/png', buffer: png });
		assert.match(publicImage.thumb_url, /\?v=[0-9a-f]{16}$/);
		await page.getByLabel('Paste imvault image links', { exact: true }).fill(publicImage.thumb_url);
		await page.getByRole('button', { name: 'Start conversation', exact: true }).click();
		await page.waitForURL(/\/topics\/\d+$/);
		const topicURL = page.url();
		const beforeSlowLibrary = libraryRequests;
		stallLibrary = true;
		try {
			for (const url of [topicURL, origin + '/boards/1/new']) {
				const response = await context.request.get(url, { timeout: 5000 });
				assert.equal(response.status(), 200, 'A stalled image library prevented reading or writing text');
			}
			assert.equal(libraryRequests, beforeSlowLibrary, 'Text pages requested the stalled library');
		} finally {
			stallLibrary = false;
		}
		await page.locator('.post-images img').first().waitFor();
		assert.equal(await page.locator('.post-images img').count(), 3);
		for (const img of await page.locator('.post-images img').all()) {
			await img.scrollIntoViewIfNeeded();
			await img.evaluate(el => el.decode());
		}
		const paths = await page.locator('.post-images img').evaluateAll(images => images.map(img => img.getAttribute('src')));
		const library = await (await fetch(vaultURL + '/api/v1/files?q=New-table', { headers: auth })).json();
		assert.ok(library.files.length > 0);
		assert.ok(library.files.every(file => file.visibility === 'private' && file.metadata === 'hidden'), 'Witmoot uploads were exposed in imvault');
		const guest = await browser.newContext({ javaScriptEnabled });
		assert.equal((await guest.request.get(origin + paths[0], { maxRedirects: 0 })).status(), 303);
		await page.getByLabel('Your reply', { exact: true }).fill('Another look at the same memory.');
		await page.getByText('Add images', { exact: true }).click();
		await page.getByRole('button', { name: 'Load image library', exact: true }).click();
		await page.locator('.image-choice input[type=checkbox]').first().waitFor();
		assert.equal(await page.getByLabel('Your reply', { exact: true }).inputValue(), 'Another look at the same memory.', 'Loading images lost the reply');
		await page.getByLabel('Paste imvault image links', { exact: true }).fill(privateImage.thumb_url.replace('/thumb?', '/preview?'));
		await page.getByRole('button', { name: 'Post reply', exact: true }).click();
		await page.waitForURL(/#post-/);
		assert.equal(await page.locator('.post-images img').count(), 4);
		if (process.env.WITMOOT_SCREENSHOT_DIR && javaScriptEnabled) {
			await mkdir(process.env.WITMOOT_SCREENSHOT_DIR, { recursive: true });
			await page.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, 'images-desktop.png'), fullPage: true });
			await page.setViewportSize({ width: 390, height: 844 });
			assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Image topic overflows on mobile');
			await page.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, 'images-mobile.png'), fullPage: true });
		}
		await page.goto(origin + '/account/imvault');
		await page.getByRole('button', { name: 'Disconnect imvault', exact: true }).click();
		await page.waitForURL(origin + '/account/imvault?saved=1');
		assert.equal((await context.request.get(origin + paths[0])).status(), 404, 'Disconnect left a private image accessible');
		assert.equal((await context.request.get(origin + paths[1])).status(), 200, 'Public pasted image should survive disconnect');
		await page.goto(topicURL);
		assert.equal(await page.locator('.post').count(), 2, 'Disconnect changed the conversation');
		await guest.close();
		await context.close();
		console.log(`PASS: real imvault API, ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} connection, library, upload, pasted links, replies, privacy, and disconnect`);
	}
	await vaultContext.close();
	assert.deepEqual(problems, [], 'Browser script or CSP errors');
} finally {
	if (browser) await browser.close();
	for (const child of children.reverse()) {
		if (child.exitCode === null) {
			const exited = new Promise(done => child.once('exit', done));
			child.kill('SIGTERM');
			await exited;
		}
	}
	if (gateway?.listening) {
		gateway.closeAllConnections();
		await new Promise(done => gateway.close(done));
	}
	await rm(data, { recursive: true, force: true });
}
