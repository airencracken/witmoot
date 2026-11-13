const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = readFileSync(join(__dirname, '../internal/forum/static/copy.js'), 'utf8');

function page({ clipboard = 'works', readonly = true } = {}) {
	const events = {};
	const copied = [];
	const field = {
		value: 'https://board.example/join/abc',
		focused: false, selected: false,
		hasAttribute: name => name === 'readonly' && readonly,
		focus() { this.focused = true; },
		select() { this.selected = true; },
	};
	const button = {
		hidden: true, textContent: 'Copy link',
		getAttribute: name => (name === 'data-copy' ? 'invite-link' : null),
		closest: selector => (selector === '[data-copy]' ? button : null),
	};
	const navigator = {};
	if (clipboard === 'works') navigator.clipboard = { writeText: async text => { copied.push(text); } };
	if (clipboard === 'refuses') navigator.clipboard = { writeText: async () => { throw new Error('Denied'); } };
	if (clipboard === 'broken') navigator.clipboard = {};
	const document = {
		querySelectorAll: selector => (selector === '[data-copy]' ? [button] : []),
		getElementById: id => (id === 'invite-link' ? field : null),
		addEventListener: (name, handler) => { events[name] = handler; },
	};
	vm.runInNewContext(source, { document, navigator });
	return {
		button, field, copied,
		load: (name = 'DOMContentLoaded') => events[name](),
		click: target => events.click({ target }),
	};
}

test('copy buttons appear after load or an HTMX swap when the clipboard works', () => {
	for (const event of ['DOMContentLoaded', 'htmx:afterSwap']) {
		const tab = page();
		tab.load(event);
		assert.equal(tab.button.hidden, false);
	}
});

test('without a usable clipboard the button stays hidden and the field remains', () => {
	for (const clipboard of ['missing', 'broken']) {
		const tab = page({ clipboard });
		tab.load();
		assert.equal(tab.button.hidden, true);
	}
});

test('clicking copies the field value and says so', async () => {
	const tab = page();
	tab.load();
	await tab.click(tab.button);
	assert.deepEqual(tab.copied, ['https://board.example/join/abc']);
	assert.equal(tab.button.textContent, 'Copied');
});

test('a refused copy selects the field for manual copying', async () => {
	const tab = page({ clipboard: 'refuses' });
	tab.load();
	await tab.click(tab.button);
	assert.equal(tab.field.selected, true);
	assert.equal(tab.field.focused, true);
	assert.match(tab.button.textContent, /copy/i);
});

test('only read-only fields named by a copy button are copied', async () => {
	const tab = page({ readonly: false });
	tab.load();
	await tab.click(tab.button);
	assert.deepEqual(tab.copied, []);
	const other = page();
	await other.click({ closest: () => null });
	await other.click({});
	await other.click(null);
	assert.deepEqual(other.copied, []);
});
