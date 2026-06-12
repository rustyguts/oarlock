import type { Component } from 'svelte';
import GlobeIcon from '@lucide/svelte/icons/globe';
import BracesIcon from '@lucide/svelte/icons/braces';
import TimerIcon from '@lucide/svelte/icons/timer';
import SparklesIcon from '@lucide/svelte/icons/sparkles';
import WrenchIcon from '@lucide/svelte/icons/wrench';
import PuzzleIcon from '@lucide/svelte/icons/puzzle';

// One icon per step type, shared by the palette and the canvas nodes.
const icons: Record<string, Component> = {
	'http.request': GlobeIcon,
	transform: BracesIcon,
	delay: TimerIcon,
	'ai.prompt': SparklesIcon,
	'mcp.tool': WrenchIcon
};

export function stepIcon(type: string): Component {
	return icons[type] ?? PuzzleIcon;
}
