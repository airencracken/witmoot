// Copy buttons appear only where the clipboard works. The read-only field next
// to each button stays usable on its own, with or without JavaScript.
(() => {
	function prepare() {
		if (!navigator.clipboard || typeof navigator.clipboard.writeText !== 'function') return;
		for (const button of document.querySelectorAll('[data-copy]')) button.hidden = false;
	}

	document.addEventListener('DOMContentLoaded', prepare);
	document.addEventListener('htmx:afterSwap', prepare);
	document.addEventListener('click', async event => {
		const button = event.target && event.target.closest ? event.target.closest('[data-copy]') : null;
		if (!button) return;
		const field = document.getElementById(button.getAttribute('data-copy'));
		if (!field || !field.hasAttribute('readonly')) return;
		try {
			await navigator.clipboard.writeText(field.value);
			button.textContent = 'Copied';
		} catch {
			field.focus();
			field.select();
			button.textContent = 'Press Ctrl+C to copy';
		}
	});
})();
