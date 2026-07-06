import { describe, it, expect } from 'vitest';
import { definitionToFlow, flowToDefinition, nextKey, type StepNode } from './flow';
import type { Definition } from './api';

// The JSON definition is the only canonical workflow artifact (design hard
// rule 4); nodes/edges are a projection. These tests pin the definition⇄canvas
// round-trip and document every place the projection normalizes the shape.
//
// Round-trip is EXACT (deep-equal) only for a "canonical" definition — one where
// every step carries an explicit `config` object and an integer `ui` position,
// and `retries`/`timeout` are either absent or truthy. definitionToFlow →
// flowToDefinition then reproduces the input verbatim. The remaining tests below
// isolate each normalization that breaks exactness so the behavior is a decision,
// not an accident.

function roundTrip(def: Definition): Definition {
	const { nodes, edges } = definitionToFlow(def);
	return flowToDefinition(def.name ?? '', nodes, edges);
}

describe('definition ⇄ flow round-trip', () => {
	it('reproduces a canonical multi-step definition verbatim', () => {
		const def: Definition = {
			name: 'order-sync',
			steps: [
				{
					key: 'fetch',
					type: 'http.request',
					config: { url: 'https://api.example.com/orders', method: 'GET' },
					ui: { x: 80, y: 200 }
				},
				{
					key: 'count',
					type: 'transform',
					needs: ['fetch'],
					config: { script: 'return steps.fetch.body.orders.length' },
					ui: { x: 360, y: 120 }
				},
				{
					// config value carries an interpolation expression — must survive untouched
					key: 'title',
					type: 'transform',
					needs: ['fetch'],
					config: { script: '{{ steps.fetch.body.title }}' },
					ui: { x: 360, y: 280 }
				},
				{
					// fan-in (two needs) plus retries + timeout + run-condition
					key: 'report',
					type: 'transform',
					needs: ['count', 'title'],
					retries: 2,
					timeout: 30,
					if: 'steps.count > 0',
					config: { script: 'return `${steps.title}: ${steps.count} orders`' },
					ui: { x: 640, y: 200 }
				}
			]
		};

		expect(roundTrip(def)).toEqual(def);
	});

	it('preserves needs order for a fan-in step', () => {
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', config: {}, ui: { x: 0, y: 0 } },
				{ key: 'b', type: 'noop', config: {}, ui: { x: 0, y: 100 } },
				{ key: 'c', type: 'noop', config: {}, ui: { x: 0, y: 200 } },
				// order [c, a, b] is deliberately not sorted
				{ key: 'sink', type: 'noop', needs: ['c', 'a', 'b'], config: {}, ui: { x: 300, y: 100 } }
			]
		};
		const out = roundTrip(def);
		expect(out.steps.find((s) => s.key === 'sink')?.needs).toEqual(['c', 'a', 'b']);
		expect(out).toEqual(def);
	});

	it('preserves step order', () => {
		const keys = ['z', 'm', 'a', 'q'];
		const def: Definition = {
			name: 'ordering',
			steps: keys.map((k, i) => ({
				key: k,
				type: 'noop',
				config: {},
				ui: { x: i * 100, y: 0 }
			}))
		};
		expect(roundTrip(def).steps.map((s) => s.key)).toEqual(keys);
	});

	it('handles an empty workflow (no steps, no edges)', () => {
		const def: Definition = { name: 'empty', steps: [] };
		const { nodes, edges } = definitionToFlow(def);
		expect(nodes).toEqual([]);
		expect(edges).toEqual([]);
		expect(roundTrip(def)).toEqual({ name: 'empty', steps: [] });
	});

	it('tolerates a definition with no steps array at all', () => {
		// definitionToFlow guards `def.steps ?? []`.
		const { nodes, edges } = definitionToFlow({ steps: undefined as unknown as [] });
		expect(nodes).toEqual([]);
		expect(edges).toEqual([]);
	});
});

describe('projection normalizations (round-trip is intentionally NOT exact here)', () => {
	it('materializes a missing config as an empty object', () => {
		const def: Definition = {
			name: 'wf',
			steps: [{ key: 'a', type: 'noop', ui: { x: 0, y: 0 } }]
		};
		// Input step has no `config`; the projection defaults it to {} and writes it back.
		expect(roundTrip(def).steps[0].config).toEqual({});
	});

	it('materializes a missing ui with the computed fallback position', () => {
		// definitionToFlow default: x = 80 + i*240, y = 120. flowToDefinition then
		// persists those coordinates, so a ui-less step gains a concrete ui.
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', config: {} },
				{ key: 'b', type: 'noop', config: {} }
			]
		};
		const out = roundTrip(def);
		expect(out.steps[0].ui).toEqual({ x: 80, y: 120 });
		expect(out.steps[1].ui).toEqual({ x: 320, y: 120 });
	});

	it('rounds fractional canvas positions to integers', () => {
		const nodes: StepNode[] = [
			{ id: 'a', type: 'step', position: { x: 12.4, y: 99.6 }, data: { stepType: 'noop', config: {} } }
		];
		expect(flowToDefinition('wf', nodes, []).steps[0].ui).toEqual({ x: 12, y: 100 });
	});

	it('drops falsy retries and timeout (0 / undefined are omitted)', () => {
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', retries: 0, timeout: 0, config: {}, ui: { x: 0, y: 0 } }
			]
		};
		const step = roundTrip(def).steps[0];
		expect(step.retries).toBeUndefined();
		expect(step.timeout).toBeUndefined();
		expect('retries' in step).toBe(false);
		expect('timeout' in step).toBe(false);
	});

	it('keeps truthy retries and timeout', () => {
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', retries: 3, timeout: 45, config: {}, ui: { x: 0, y: 0 } }
			]
		};
		const step = roundTrip(def).steps[0];
		expect(step.retries).toBe(3);
		expect(step.timeout).toBe(45);
	});

	it('drops an empty `if` condition (empty string is omitted)', () => {
		const def: Definition = {
			name: 'wf',
			steps: [{ key: 'a', type: 'noop', if: '', config: {}, ui: { x: 0, y: 0 } }]
		};
		const step = roundTrip(def).steps[0];
		expect(step.if).toBeUndefined();
		expect('if' in step).toBe(false);
	});

	it('keeps a non-empty `if` condition verbatim', () => {
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', if: 'input.enabled === true', config: {}, ui: { x: 0, y: 0 } }
			]
		};
		expect(roundTrip(def).steps[0].if).toBe('input.enabled === true');
	});

	it('omits an empty needs array rather than emitting needs: []', () => {
		const def: Definition = {
			name: 'wf',
			steps: [{ key: 'lonely', type: 'noop', config: {}, ui: { x: 0, y: 0 } }]
		};
		expect('needs' in roundTrip(def).steps[0]).toBe(false);
	});
});

describe('edge derivation', () => {
	it('emits one edge per need with a stable source->target id', () => {
		const def: Definition = {
			name: 'wf',
			steps: [
				{ key: 'a', type: 'noop', config: {}, ui: { x: 0, y: 0 } },
				{ key: 'b', type: 'noop', needs: ['a'], config: {}, ui: { x: 100, y: 0 } }
			]
		};
		const { edges } = definitionToFlow(def);
		expect(edges).toEqual([{ id: 'a->b', source: 'a', target: 'b' }]);
	});
});

describe('nextKey', () => {
	it('slugifies the step type and appends the first free index', () => {
		expect(nextKey('http.request', [])).toBe('http_request_1');
		expect(nextKey('HTTP Request', [])).toBe('http_request_1');
	});

	it('skips indices already taken by existing nodes', () => {
		const nodes: StepNode[] = [
			{ id: 'transform_1', type: 'step', position: { x: 0, y: 0 }, data: { stepType: 'transform', config: {} } },
			{ id: 'transform_2', type: 'step', position: { x: 0, y: 0 }, data: { stepType: 'transform', config: {} } }
		];
		expect(nextKey('transform', nodes)).toBe('transform_3');
	});
});
