// A single shared clock that ticks every 30s so relative timestamps
// ("2m ago") stay live instead of freezing at first render. Consumers read
// `now()` inside reactive contexts (templates / $derived) to re-run on tick.

let tick = $state(Date.now());
let started = false;

function ensureTicking() {
	if (started || typeof window === 'undefined') return;
	started = true;
	setInterval(() => (tick = Date.now()), 30_000);
}

export function now(): number {
	ensureTicking();
	return tick;
}
