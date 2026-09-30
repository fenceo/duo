package main

// Account switching records only which environment was written. It never
// reads or edits tasks, queued messages, sessions or handoff archives.
func nativeAccountKey(environment, engine string) string {
	return "engine_native_account:" + environment + ":" + engine
}

func (s *Store) currentEnvironmentAccount(environment, engine string) string {
	if engine == "codex" || engine == "claude" {
		return s.setting(nativeAccountKey(environment, engine))
	}
	return s.activeEngineProfile(environment, engine)
}

func (s *Store) useSyncedNativeAccount(environment, engine, profile string) error {
	return s.set(nativeAccountKey(environment, engine), profile)
}
