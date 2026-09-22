import assert from 'node:assert/strict';

export async function checkThemes(browser, origin) {
	const context = await browser.newContext({ colorScheme: 'dark', viewport: { width: 390, height: 844 } });
	try {
		const page = await context.newPage();
		const errors = [];
		page.on('pageerror', error => errors.push(error.message));
		await page.goto(origin);
		const picker = page.getByRole('combobox', { name: 'Color theme' });
		const background = () => page.locator('html').evaluate(el => getComputedStyle(el).backgroundColor);
		assert.equal(await picker.inputValue(), 'system');
		assert.equal(await background(), 'rgb(23, 31, 26)');
		await page.emulateMedia({ colorScheme: 'light' });
		assert.equal(await background(), 'rgb(245, 243, 234)');
		await picker.selectOption('dark');
		await page.reload();
		assert.equal(await picker.inputValue(), 'dark');
		assert.equal(await background(), 'rgb(23, 31, 26)');

		// Navigation replaces the body through HTMX, including the theme selector.
		await page.evaluate(() => { window.themeNavigationMarker = true; });
		await page.getByRole('link', { name: 'Come on in', exact: false }).first().click();
		await page.waitForURL(origin + '/login');
		await picker.waitFor({ state: 'visible' });
		assert.equal(await page.evaluate(() => window.themeNavigationMarker), true);
		assert.equal(await picker.inputValue(), 'dark');
		assert.equal(await background(), 'rgb(23, 31, 26)');
		await picker.selectOption('light');
		await page.emulateMedia({ colorScheme: 'dark' });
		assert.equal(await background(), 'rgb(245, 243, 234)');
		assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);

		const other = await context.newPage();
		await other.goto(origin);
		await other.getByRole('combobox', { name: 'Color theme' }).selectOption('dark');
		await page.waitForFunction(() => document.querySelector('[data-theme-select]').value === 'dark');
		assert.equal(await background(), 'rgb(23, 31, 26)');
		await picker.selectOption('system');
		assert.equal(await page.evaluate(() => localStorage.getItem('witmoot-theme')), null);
		await page.emulateMedia({ colorScheme: 'light' });
		assert.equal(await background(), 'rgb(245, 243, 234)');
		assert.deepEqual(errors, []);
	} finally {
		await context.close();
	}

	const noJS = await browser.newContext({ javaScriptEnabled: false, colorScheme: 'dark' });
	try {
		const page = await noJS.newPage();
		await page.goto(origin);
		assert.equal(await page.locator('html').evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(23, 31, 26)');
		assert.equal(await page.getByRole('combobox', { name: 'Color theme' }).isVisible(), false);
	} finally {
		await noJS.close();
	}
	console.log('PASS: themes follow system changes, persist, survive HTMX, sync across tabs, and work without JavaScript');
}
