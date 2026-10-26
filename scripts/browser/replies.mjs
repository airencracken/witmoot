import assert from 'node:assert/strict';

export async function checkReplies(page, javaScriptEnabled) {
	await page.goto(new URL('/boards/1/new', page.url()).href);
	await page.getByLabel('Give it a title').fill(`Reply refresh ${javaScriptEnabled ? 'with HTMX' : 'without JavaScript'}`);
	await page.getByLabel('Your message').fill('Starting a conversation for reply regression coverage.');
	await page.getByRole('button', { name: 'Start conversation', exact: true }).click();
	await page.waitForURL(/\/topics\/\d+$/);
	const topicURL = page.url();
	const replyURL = `${topicURL}/replies`;
	let submissions = 0;
	page.on('request', request => {
		if (request.method() === 'POST' && request.url() === replyURL) submissions++;
	});
	async function reply(body, count) {
		await page.getByLabel('Your reply').fill(body);
		await page.getByRole('button', { name: 'Post reply', exact: true }).click();
		await page.locator(':target .post-body').filter({ hasText: body }).waitFor();
		assert.equal(await page.locator('.post').count(), count);
		assert.equal(await page.getByLabel('Your reply').inputValue(), '', 'Successful reply clears the draft');
		assert.equal(await page.getByRole('button', { name: 'Post reply', exact: true }).isEnabled(), true);
	}
	await reply('The first reply opens the permalink.', 2);
	// Only the fragment changes on consecutive replies or after following a permalink.
	await reply('The second reply must refresh the same page.', 3);
	await page.goto(page.url());
	await reply('Replying from a loaded permalink also refreshes.', 4);
	if (javaScriptEnabled) {
		let releaseResponse;
		const heldResponse = new Promise(resolve => { releaseResponse = resolve; });
		let responseReady;
		const ready = new Promise(resolve => { responseReady = resolve; });
		await page.route(replyURL, async route => {
			const response = await route.fetch();
			responseReady();
			await heldResponse;
			await route.fulfill({ response });
		});
		await page.getByLabel('Your reply').fill('One submission while the response is pending.');
		const before = submissions;
		await page.getByRole('button', { name: 'Post reply', exact: true }).click();
		await ready;
		try {
			assert.equal(await page.getByRole('button', { name: 'Post reply', exact: true }).isDisabled(), true);
			// Enter and other submit events must not queue another write while busy.
			await page.locator('#reply form').evaluate(form => form.requestSubmit());
		} finally {
			releaseResponse();
		}
		await page.locator(':target .post-body').filter({ hasText: 'One submission while the response is pending.' }).waitFor();
		await page.unroute(replyURL);
		await page.reload();
		assert.equal(submissions - before, 1, 'Busy form drops repeated submissions');
		assert.equal(await page.locator('.post').count(), 5, 'Repeated submit produces exactly one reply');
	}
	// Fill page one, then check navigation to a new page and another same-page reply.
	const csrf = await page.locator('#reply input[name="csrf"]').inputValue();
	for (let count = javaScriptEnabled ? 5 : 4; count < 20; count++) {
		const response = await page.request.post(replyURL, { form: { csrf, body: `Page boundary seed ${count}` } });
		assert.equal(response.status(), 200);
	}
	await page.reload();
	await reply('This reply starts the second page.', 1);
	assert.equal(new URL(page.url()).search, '?page=2');
	await reply('The next reply refreshes the second page.', 2);
	console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} consecutive replies refresh and clear the form`);
}
