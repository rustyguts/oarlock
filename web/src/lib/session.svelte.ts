import { api, ApiError, type Me } from './api';

// Global auth gate. The root layout consults `status` to decide whether to
// render the app, the login/setup screen, or a forced password change. Every
// page renders inside the layout, so this is the single choke point.

export type AuthStatus = 'loading' | 'authed' | 'login' | 'setup' | 'change-password' | 'error';

let me = $state<Me | null>(null);
let status = $state<AuthStatus>('loading');
let error = $state('');

export const session = {
	get me() {
		return me;
	},
	get status() {
		return status;
	},
	get error() {
		return error;
	},

	// Resolve the current auth state from /v1/me. 401 → login (or setup on a
	// fresh instance); a must_change_password session → forced change.
	async refresh(): Promise<void> {
		try {
			const res = await api.me();
			me = res;
			status = res.must_change_password ? 'change-password' : 'authed';
			error = '';
		} catch (e) {
			me = null;
			if (e instanceof ApiError && e.status === 401) {
				status = e.setupRequired ? 'setup' : 'login';
				error = '';
			} else {
				status = 'error';
				error = e instanceof Error ? e.message : String(e);
			}
		}
	},

	async logout(): Promise<void> {
		try {
			await api.logout();
		} catch {
			/* clearing the cookie is best-effort */
		}
		me = null;
		status = 'login';
	}
};
