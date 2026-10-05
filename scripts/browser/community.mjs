import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkCommunity(browser, origin, password, javaScriptEnabled) {
	const contexts = [];
	const problems = [];
	const suffix = javaScriptEnabled ? 'js' : 'plain';
	const username = `care-${suffix}`;
	async function newPage() {
		const context = await browser.newContext({ javaScriptEnabled, colorScheme: 'light', viewport: { width: 390, height: 844 } });
		contexts.push(context);
		const page = await context.newPage();
		page.on('pageerror', error => problems.push(error.message));
		page.on('console', message => { if (message.type() === 'error' && /Content Security Policy|Refused to/.test(message.text())) problems.push(message.text()); });
		return page;
	}
	async function signIn(page, name) {
		await page.goto(origin + '/login');
		await page.getByLabel('Username', { exact: true }).fill(name);
		await page.getByLabel('Password', { exact: true }).fill(password);
		await page.getByRole('button', { name: 'Sign in', exact: true }).click();
	}
	async function screenshot(page, name) {
		for (const colorScheme of (javaScriptEnabled ? ['light', 'dark'] : ['light'])) {
			await page.emulateMedia({ colorScheme });
			for (const width of (javaScriptEnabled ? [320, 390, 768, 1280] : [390])) {
				await page.setViewportSize({ width, height: 844 });
				assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, `${name} overflows at ${width}px in ${colorScheme}`);
				if (process.env.WITMOOT_SCREENSHOT_DIR) {
					await mkdir(process.env.WITMOOT_SCREENSHOT_DIR, { recursive: true });
					await page.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, `${name}-${suffix}-${colorScheme}-${width}.png`), fullPage: true });
				}
			}
		}
		await page.emulateMedia({ colorScheme: 'light' });
		await page.setViewportSize({ width: 390, height: 844 });
	}
	try {
		const owner = await newPage();
		await signIn(owner, 'alex');
		await owner.waitForURL(origin + '/');
		await owner.getByRole('link', { name: 'Your account', exact: true }).waitFor({ state: 'visible' });
		await owner.getByText('Manage', { exact: true }).focus();
		await owner.getByText('Manage', { exact: true }).press('Enter');
		await owner.getByRole('link', { name: 'Settings', exact: true }).click();
		await owner.getByRole('radio', { name: /^Private/ }).check();
		await owner.getByLabel('House rules (optional)', { exact: true }).fill('Ask before sharing someone else\'s photos.\nKeep private conversations here.');
		await owner.getByLabel('How to contact the owners (optional)', { exact: true }).fill('Ask alex, or visit https://example.org/contact.');
		await owner.getByRole('button', { name: 'Save settings', exact: true }).click();
		await owner.waitForURL(origin + '/settings?saved=1');
		const visitor = await newPage();
		await visitor.goto(origin + '/about');
		await visitor.getByRole('heading', { name: 'House rules', exact: true }).waitFor();
		assert.match(await visitor.locator('main').innerText(), /Ask before sharing/);
		assert.equal(await visitor.getByRole('link', { name: 'Edit rules and contact details' }).count(), 0);
		await screenshot(visitor, 'house-rules');
		await owner.goto(origin + '/invites');
		await owner.getByRole('button', { name: 'Create an invitation', exact: true }).click();
		const invitation = await owner.getByRole('link', { name: 'Invitation link', exact: true }).getAttribute('href');
		const member = await newPage();
		await member.goto(invitation);
		await member.getByLabel('Username', { exact: true }).fill(username);
		await member.getByLabel('Password', { exact: true }).fill(password);
		await member.getByRole('button', { name: 'Join our board', exact: true }).click();
		await member.waitForURL(origin + '/');
		await member.getByRole('link', { name: 'Your account', exact: true }).waitFor({ state: 'visible' });
		await member.goto(origin + '/boards/1/new');
		await member.getByLabel('Give it a title').fill(`A shared plan ${suffix}`);
		await member.getByLabel('Your message', { exact: true }).fill('A message for the owner to remove.');
		await member.getByRole('button', { name: 'Start conversation', exact: true }).click();
		await member.waitForURL(/\/topics\/\d+$/);
		const topicURL = member.url();
		assert.equal(await member.getByRole('link', { name: 'Remove message 1', exact: true }).count(), 0);
		await member.getByLabel('Your reply').fill('A reply that should stay here.');
		await member.getByRole('button', { name: 'Post reply', exact: true }).click();
		await member.waitForURL(/#post-/);
		await owner.goto(topicURL);
		await owner.getByRole('link', { name: 'Remove message 1', exact: true }).click();
		await owner.getByRole('checkbox', { name: 'Also replace the conversation title', exact: false }).check();
		await owner.getByRole('checkbox', { name: 'I understand this message cannot be restored.', exact: true }).check();
		await screenshot(owner, 'remove-message');
		await owner.getByRole('button', { name: 'Remove message', exact: true }).click();
		await owner.waitForURL(/#post-/);
		assert.equal(await owner.locator('h1').innerText(), 'Conversation');
		assert.equal(await owner.locator('.removed-message').innerText(), 'This message was removed by a site owner.');
		assert.equal(await owner.locator('.post-body').innerText(), 'A reply that should stay here.');
		await member.goto(topicURL);
		assert.equal(await member.getByRole('link', { name: 'Edit message 1', exact: true }).count(), 0);
		assert.match(await member.locator('main').innerText(), /reply that should stay/);
		await owner.goto(origin + '/members');
		const record = owner.locator('.invitation-record').filter({ has: owner.getByRole('heading', { name: username, exact: true }) });
		await record.getByRole('link', { name: `Suspend member ${username}`, exact: true }).click();
		await owner.getByLabel(`Type ${username} to confirm`, { exact: true }).fill(username);
		await screenshot(owner, 'suspend-member');
		await owner.getByRole('button', { name: 'Suspend member', exact: true }).click();
		await owner.waitForURL(origin + '/members?saved=suspend');
		await member.goto(topicURL);
		await member.waitForURL(origin + '/login');
		await signIn(member, username);
		await member.getByRole('alert').waitFor();
		assert.match(await member.getByRole('alert').innerText(), /account is suspended/);
		await screenshot(owner, 'members');
		const suspendedRecord = owner.locator('.invitation-record').filter({ has: owner.getByRole('heading', { name: new RegExp(`^${username}`) }) });
		await suspendedRecord.getByRole('link', { name: `Restore access for ${username}`, exact: true }).click();
		await owner.getByLabel(`Type ${username} to confirm`, { exact: true }).fill(username);
		await owner.getByRole('button', { name: 'Restore member access', exact: true }).click();
		await owner.waitForURL(origin + '/members?saved=restore');
		await signIn(member, username);
		await member.waitForURL(origin + '/');
		await member.goto(topicURL);
		await member.getByRole('button', { name: 'Post reply', exact: true }).waitFor();
		await owner.getByRole('link', { name: 'House rules & owners', exact: true }).click();
		await owner.getByRole('link', { name: 'Edit rules and contact details', exact: true }).waitFor();
		await owner.goto(origin + '/moderation');
		assert.match(await owner.locator('main').innerText(), new RegExp(`Suspended ${username}`));
		assert.match(await owner.locator('main').innerText(), /Removed message \d+ in conversation \d+/);
		await screenshot(owner, 'owner-activity');
		await owner.goto(topicURL);
		await owner.getByLabel('Your reply').fill('A reply from the owner that survives departure.');
		await owner.getByRole('button', { name: 'Post reply', exact: true }).click();
		await owner.waitForURL(/#post-/);
		const stamp = owner.locator('.post-content header time').first();
		assert.ok(await stamp.getAttribute('datetime'), 'display time keeps its machine-readable value');
		await owner.goto(origin + '/settings');
		const downloadEvent = owner.waitForEvent('download');
		await owner.getByRole('link', { name: 'Download community archive', exact: true }).click();
		const download = await downloadEvent;
		assert.equal(await download.failure(), null);
		assert.match(download.suggestedFilename(), /^witmoot-community-/);
		await member.goto(origin + '/account/delete');
		await member.getByLabel('Type your username:', { exact: false }).fill(username);
		await member.getByLabel('Current password', { exact: true }).fill(password);
		await screenshot(member, 'delete-account');
		await member.getByRole('button', { name: 'Delete my account', exact: true }).click();
		await member.waitForURL(origin + '/login?deleted=1');
		await owner.goto(topicURL);
		assert.match(await owner.locator('main').innerText(), /This message was deleted by its author/);
		assert.match(await owner.locator('main').innerText(), /A reply from the owner that survives departure/);
		assert.doesNotMatch(await owner.locator('main').innerText(), new RegExp(username));
		assert.deepEqual(problems, [], 'Community flow script or CSP errors');
		console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} mobile rules, account access, message removal, suspension, restoration, owner activity, community export, and account deletion`);
	} finally {
		for (const context of contexts) await context.close();
	}
}
