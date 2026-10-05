(function (root) {
	function localStamp(value, locale) {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return null;
		return new Intl.DateTimeFormat(locale, {
			year: 'numeric', month: 'short', day: 'numeric',
			hour: '2-digit', minute: '2-digit', timeZoneName: 'short'
		}).format(date);
	}
	function updateTimes(scope) {
		scope.querySelectorAll('time[data-local-time][datetime]').forEach(function (element) {
			const text = localStamp(element.getAttribute('datetime'));
			if (text) element.textContent = text;
		});
	}
	if (typeof module !== 'undefined') module.exports = { localStamp, updateTimes };
	if (root.document) {
		root.document.addEventListener('DOMContentLoaded', function () { updateTimes(root.document); });
		root.document.addEventListener('htmx:afterSwap', function () { updateTimes(root.document); });
	}
})(typeof window !== 'undefined' ? window : globalThis);
