import assert from 'node:assert/strict';
import { join } from 'node:path';

export async function checkGroups(browser, origin, password, javaScriptEnabled) {
	const contexts = [];
	const errors = [];
	async function signIn(username) {
		const context = await browser.newContext({ javaScriptEnabled, colorScheme: javaScriptEnabled ? 'dark' : 'light', viewport: { width: 1280, height: 900 } });
		contexts.push(context);
		context.on('page', page => {
			page.on('pageerror', error => errors.push(error.message));
			page.on('console', message => { if (message.type() === 'error' && /Content Security Policy|Refused to/.test(message.text())) errors.push(message.text()); });
		});
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
		await owner.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Manage groups', exact: true }).click();
		await owner.getByRole('link', { name: 'Create a group', exact: true }).click();
		const groupName = javaScriptEnabled ? 'Soup club with HTMX' : 'Soup club without scripts';
		await owner.getByLabel('Group name', { exact: true }).fill(groupName);
		await owner.getByLabel('Description', { exact: true }).fill('Soup, stories, and just enough chairs.');
		for (const member of ['jules', 'sam', 'robin']) await owner.getByRole('checkbox', { name: member, exact: true }).check();
		assert.equal(await owner.getByRole('checkbox', { name: 'alex', exact: true }).count(), 0);
		await owner.getByRole('button', { name: 'Create group', exact: true }).click();
		await owner.waitForURL(/\/groups\/\d+\?saved=1$/);
		const groupURL = owner.url().replace('?saved=1', '');
		await owner.getByRole('status').waitFor();

		await owner.goto(origin + '/boards/new');
		const boardName = javaScriptEnabled ? 'Soup plans with HTMX' : 'Soup plans without scripts';
		await owner.getByLabel('Board name', { exact: true }).fill(boardName);
		await owner.getByLabel('Category', { exact: true }).fill('Good company');
		await owner.getByLabel(`Group: ${groupName}`).selectOption('write');
		await owner.getByLabel('sam', { exact: true }).selectOption('read');
		await owner.getByLabel('robin', { exact: true }).selectOption('none');
		assert.equal(await owner.getByLabel('jules', { exact: true }).inputValue(), 'inherit');
		await owner.getByRole('button', { name: 'Create board', exact: true }).click();
		await owner.waitForURL(/\/boards\/\d+\/settings\?saved=1$/);
		const boardURL = owner.url().replace('/settings?saved=1', '');
		await owner.goto(boardURL + '/new');
		await owner.getByLabel('Give it a title').fill('Who is bringing the soup?');
		await owner.getByLabel('Your message', { exact: true }).fill('A conversation for our group.');
		await owner.getByRole('button', { name: 'Start conversation', exact: true }).click();
		await owner.waitForURL(/\/topics\/\d+$/);
		const topicURL = owner.url();
		const writer = await signIn('jules');
		const reader = await signIn('sam');
		const denied = await signIn('robin');
		assert.equal(await writer.getByRole('link', { name: 'Manage groups', exact: true }).count(), 0);
		assert.equal((await denied.goto(topicURL)).status(), 404);
		await reader.goto(topicURL);
		assert.equal(await reader.getByRole('button', { name: 'Post reply', exact: true }).count(), 0);
		await writer.goto(topicURL);
		await writer.getByLabel('Your reply', { exact: true }).fill('I can bring lentil soup.');
		await writer.getByRole('button', { name: 'Post reply', exact: true }).click();
		await writer.waitForURL(/#post-/);

		await owner.goto(groupURL);
		const row = username => owner.locator('.access-report tbody tr').filter({ has: owner.getByRole('rowheader', { name: username, exact: true }) });
		assert.match(await row('jules').innerText(), /Use groups.*Read and post/s);
		assert.match(await row('sam').innerText(), /Read only.*Individual override.*Read only/s);
		assert.match(await row('robin').innerText(), /No access.*Individual override.*No access/s);
		await writer.getByRole('link', { name: 'Edit message 2', exact: true }).click();
		await writer.waitForURL(/\/posts\/\d+\/edit$/);
		await owner.getByRole('checkbox', { name: 'jules', exact: true }).uncheck();
		await owner.getByRole('button', { name: 'Save group', exact: true }).click();
		await owner.waitForURL(groupURL + '?saved=1');
		await owner.getByRole('status').waitFor();
		assert.equal(await row('jules').count(), 0);
		await writer.getByLabel('Your message', { exact: true }).fill('A stale group edit');
		await writer.getByRole('button', { name: 'Save changes', exact: true }).click();
		await writer.getByRole('heading', { name: 'Not Found', exact: true }).waitFor();
		assert.equal((await writer.goto(topicURL)).status(), 404);
		await owner.getByRole('checkbox', { name: 'jules', exact: true }).check();
		await owner.getByRole('button', { name: 'Save group', exact: true }).click();
		await row('jules').waitFor();
		await writer.goto(topicURL);
		await writer.getByRole('button', { name: 'Post reply', exact: true }).waitFor();
		assert.match(await writer.locator('.post-body').last().innerText(), /lentil soup/);

		await owner.goto(boardURL + '/settings');
		await owner.getByLabel(`Group: ${groupName}`).selectOption('read');
		await owner.getByLabel('jules', { exact: true }).selectOption('write');
		await owner.getByRole('button', { name: 'Save board', exact: true }).click();
		await owner.getByRole('status').waitFor();
		await owner.goto(groupURL);
		assert.match(await row('jules').innerText(), /Read and post.*Individual override.*Read and post/s);
		const stale = await owner.context().newPage();
		await stale.goto(groupURL);
		const renamed = groupName + ' regulars';
		await owner.getByLabel('Group name', { exact: true }).fill(renamed);
		await owner.getByRole('button', { name: 'Save group', exact: true }).click();
		await owner.getByRole('status').waitFor();
		await stale.getByLabel('Group name', { exact: true }).fill('A stale rename');
		await stale.getByRole('button', { name: 'Save group', exact: true }).click();
		await stale.getByRole('alert').waitFor();
		assert.match(await stale.getByRole('alert').innerText(), /group has changed/);
		assert.equal(await stale.getByLabel('Group name', { exact: true }).inputValue(), 'A stale rename');
		await stale.close();
		if (process.env.WITMOOT_SCREENSHOT_DIR) await owner.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, `group-access-${javaScriptEnabled ? 'dark' : 'light-nojs'}.png`), fullPage: true });
		await owner.setViewportSize({ width: 390, height: 844 });
		assert.equal(await owner.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Group report overflows on mobile');
		if (process.env.WITMOOT_SCREENSHOT_DIR) await owner.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, `group-access-mobile-${javaScriptEnabled ? 'dark' : 'light-nojs'}.png`), fullPage: true });

		await owner.getByRole('link', { name: 'Delete group', exact: true }).click();
		await owner.getByRole('heading', { name: 'Delete group', exact: true }).waitFor();
		await owner.getByRole('link', { name: 'Cancel', exact: true }).click();
		await owner.waitForURL(groupURL);
		assert.equal(await owner.getByLabel('Group name', { exact: true }).inputValue(), renamed);
		await owner.getByRole('link', { name: 'Delete group', exact: true }).click();
		await owner.getByLabel('Type the group name to confirm', { exact: true }).fill('Wrong group');
		await owner.getByRole('button', { name: 'Delete group', exact: true }).click();
		await owner.getByRole('alert').waitFor();
		assert.match(await owner.getByRole('alert').innerText(), /type the group name exactly/);
		await owner.getByLabel('Type the group name to confirm', { exact: true }).fill(renamed);
		await owner.getByRole('button', { name: 'Delete group', exact: true }).click();
		await owner.waitForURL(origin + '/groups?deleted=1');
		await owner.getByRole('status').waitFor();
		assert.equal((await owner.goto(groupURL)).status(), 404);
		await writer.goto(topicURL);
		await writer.getByRole('button', { name: 'Post reply', exact: true }).waitFor();
		assert.equal((await reader.goto(topicURL)).status(), 200);
		assert.equal((await denied.goto(topicURL)).status(), 404);
		assert.deepEqual(errors, []);
		console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'No JavaScript'} groups, individual overrides, visible effective access, membership changes, stale forms, and confirmed deletion`);
	} finally {
		for (const context of contexts) await context.close();
	}
}
