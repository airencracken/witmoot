import assert from 'node:assert/strict';
import { join } from 'node:path';

export async function checkLifecycle({ owner, reader, outsider, guest, origin, boardURL, topicURL, name, javaScriptEnabled }) {
	const settingsURL = boardURL + '/settings';
	await owner.goto(topicURL);
	const editURL = await owner.getByRole('link', { name: 'Edit message 1', exact: true }).getAttribute('href');
	const draft = await owner.context().newPage();
	await draft.goto(origin + editURL);
	const staleDelete = await owner.context().newPage();
	await staleDelete.goto(boardURL + '/delete');
	await owner.goto(settingsURL);
	await owner.getByRole('link', { name: 'Archive board', exact: true }).click();
	await owner.getByRole('heading', { name: 'Archive board', exact: true }).waitFor();
	await owner.getByRole('link', { name: 'Cancel', exact: true }).click();
	await owner.waitForURL(settingsURL);
	assert.equal(await owner.locator('.archive-notice').count(), 0);
	await owner.getByRole('link', { name: 'Archive board', exact: true }).click();
	await owner.getByRole('button', { name: 'Archive board', exact: true }).click();
	await owner.waitForURL(settingsURL);
	await owner.locator('.archive-notice').waitFor();
	await staleDelete.getByLabel('Type the board name to confirm', { exact: true }).fill(name);
	await staleDelete.getByRole('button', { name: 'Delete board permanently', exact: true }).click();
	await staleDelete.getByRole('alert').waitFor();
	assert.match(await staleDelete.getByRole('alert').innerText(), /reload the confirmation page/);
	await draft.getByLabel('Your message', { exact: true }).fill('An edit submitted after archiving');
	await draft.getByRole('button', { name: 'Save changes', exact: true }).click();
	await draft.getByRole('alert').waitFor();
	assert.match(await draft.getByRole('alert').innerText(), /board is archived/);

	await reader.goto(origin + '/');
	assert.equal(await reader.getByRole('link', { name, exact: true }).count(), 0);
	await reader.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Archive', exact: true }).click();
	await reader.getByRole('link', { name, exact: true }).waitFor();
	await reader.goto(origin + '/recent');
	assert.equal(await reader.getByRole('link', { name: 'A surprise for our friends', exact: true }).count(), 0);
	await reader.goto(origin + '/search?q=surprise');
	const archivedTopic = reader.locator('.topic-row').filter({ has: reader.locator(`a[href="${new URL(topicURL).pathname}"]`) });
	assert.match(await archivedTopic.innerText(), /Archived/);
	for (const denied of [outsider, guest]) {
		await denied.goto(origin + '/archive');
		assert.equal(await denied.getByRole('link', { name, exact: true }).count(), 0);
		assert.equal((await denied.goto(topicURL)).status(), 404);
	}
	for (const allowed of [owner, reader]) {
		await allowed.goto(topicURL);
		assert.match(await allowed.locator('.archive-notice').innerText(), /posting and editing are paused/);
		assert.equal(await allowed.getByRole('button', { name: 'Post reply', exact: true }).count(), 0);
		assert.equal(await allowed.getByRole('link', { name: /^Edit message/ }).count(), 0);
	}
	await owner.goto(settingsURL);
	await owner.getByRole('link', { name: 'Restore board', exact: true }).click();
	await owner.getByRole('button', { name: 'Restore board', exact: true }).click();
	await owner.waitForURL(settingsURL);
	assert.equal(await owner.locator('.archive-notice').count(), 0);
	await owner.goto(topicURL);
	await owner.getByRole('link', { name: 'Edit message 1', exact: true }).waitFor();
	assert.match(await owner.locator('.post-body').first().innerText(), /This stays in our private room/);
	await reader.goto(topicURL);
	assert.equal(await reader.getByRole('button', { name: 'Post reply', exact: true }).count(), 0);

	await owner.goto(settingsURL);
	await owner.getByRole('link', { name: 'Delete board permanently', exact: true }).click();
	await owner.getByLabel('Type the board name to confirm', { exact: true }).fill('Wrong name');
	await owner.getByRole('button', { name: 'Delete board permanently', exact: true }).click();
	await owner.getByRole('alert').waitFor();
	assert.match(await owner.getByRole('alert').innerText(), /type the board name exactly/);
	assert.match(await owner.locator('.board-confirmation').innerText(), /1 conversation, 2 messages, and 0 attachment links/);
	if (javaScriptEnabled) assert.equal(await owner.getByRole('alert').evaluate(el => el === document.activeElement), true);
	assert.equal(await owner.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Board deletion form overflows');
	if (process.env.WITMOOT_SCREENSHOT_DIR) await owner.screenshot({ path: join(process.env.WITMOOT_SCREENSHOT_DIR, `board-delete-${javaScriptEnabled ? 'dark' : 'nojs'}.png`), fullPage: true });
	await owner.getByRole('link', { name: 'Cancel', exact: true }).click();
	await owner.waitForURL(settingsURL);
	assert.equal((await reader.goto(topicURL)).status(), 200);
	await owner.getByRole('link', { name: 'Delete board permanently', exact: true }).click();
	await owner.getByLabel('Type the board name to confirm', { exact: true }).fill(name);
	await owner.getByRole('button', { name: 'Delete board permanently', exact: true }).click();
	await owner.waitForURL(origin + '/boards/manage?deleted=1');
	assert.match(await owner.getByRole('status').innerText(), /permanently deleted/);
	assert.equal(await owner.getByRole('link', { name, exact: true }).count(), 0);
	assert.equal((await reader.goto(topicURL)).status(), 404);
	assert.equal((await reader.goto(boardURL)).status(), 404);
	await staleDelete.close();
	await draft.close();
	console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'No JavaScript'} archive, restore, private access, stale confirmations, cancel, and permanent deletion`);
}
