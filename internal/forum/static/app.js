// Keep validation messages usable for enhanced forms and ordinary navigation.
document.addEventListener('htmx:beforeSwap', function (event) {
	if ([400, 403, 404, 422, 429, 500].includes(event.detail.xhr.status)) {
		event.detail.shouldSwap = true;
		event.detail.isError = false;
	}
});

function preparePage() {
	const error = document.querySelector('[role="alert"]');
	if (error) error.focus();
}

document.addEventListener('htmx:afterSwap', preparePage);
document.addEventListener('DOMContentLoaded', preparePage);
window.addEventListener('pageshow', function (event) {
	if (event.persisted) window.location.reload();
});
