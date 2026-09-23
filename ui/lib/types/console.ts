// The shared dashboard shell consumes only these fields, regardless of its data source.
export interface ConsoleConfig {
	is_db_connected: boolean;
	is_logs_connected: boolean;
	// OSS may omit the label; EE returns null when it is unset.
	env_label?: string | null;
	restart_required?: {
		required: boolean;
		// Only the OSS configuration response supplies a reason.
		reason?: string;
	};
}

export interface ConsoleConfigQueryOptions {
	skip?: boolean;
}

// Preserve RTK Query's cached data alongside refresh errors for the existing shell behavior.
export interface ConsoleConfigQueryResult {
	data?: ConsoleConfig;
	error?: unknown;
	isLoading: boolean;
	isFetching: boolean;
	refetch: () => void;
}