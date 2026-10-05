const { test } = require('node:test');
const assert = require('node:assert/strict');
const { localStamp, updateTimes } = require('../internal/forum/static/local-time.js');
test('timestamps follow the reader timezone across a date boundary', () => {
	process.env.TZ = 'America/Los_Angeles';
	assert.match(localStamp('2026-01-02T01:04:00Z', 'en-US'), /Jan 1, 2026/);
	assert.match(localStamp('2026-01-02T01:04:00Z', 'en-US'), /05:04 PM PST/);
	assert.equal(localStamp('not a date'), null);
});
test('invalid timestamps retain their accessible server fallback', () => {
	const elements = ['2026-01-02T01:04:00Z', 'broken'].map(value => ({textContent:'fallback',getAttribute:()=>value}));
	updateTimes({querySelectorAll:selector => { assert.equal(selector,'time[data-local-time][datetime]'); return elements; }});
	assert.notEqual(elements[0].textContent,'fallback');
	assert.equal(elements[1].textContent,'fallback');
});
