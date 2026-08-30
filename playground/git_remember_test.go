// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

package playground

import "testing"

// The "remember on this device" opt-in carries the token outward ONLY through
// the host hook, and only while the switch is on. Flipping it on hands the
// current token out; changing the token while on re-stores it; flipping it off
// asks the host to drop it. There is deliberately no way to read the token back
// out of the panel except this hook (no GitToken getter).
func TestGitRememberHookFlow(t *testing.T) {
	s, _, _ := withGit(t)
	s.git.ensureWidgets() // wires the switch → hook subscription

	type call struct {
		store bool
		token string
	}
	var calls []call
	s.SetGitRememberHook(func(store bool, token string) {
		calls = append(calls, call{store, token})
	})

	// A token is entered, but with the switch off nothing leaves the panel.
	s.SetGitToken("ghp_secret")
	if len(calls) != 0 {
		t.Fatalf("token change while remember-off must not call the hook, got %+v", calls)
	}

	// Flip the switch on: the CURRENT token is handed out to be stored.
	s.git.rememberSwitch.On().Set(true)
	if len(calls) != 1 || !calls[0].store || calls[0].token != "ghp_secret" {
		t.Fatalf("turning remember on = %+v, want one {store:true, ghp_secret}", calls)
	}

	// Changing the token while on re-stores the new value.
	s.SetGitToken("ghp_rotated")
	if len(calls) != 2 || !calls[1].store || calls[1].token != "ghp_rotated" {
		t.Fatalf("token change while on = %+v, want re-store of ghp_rotated", calls)
	}

	// Flipping it off asks the host to drop the stored credential.
	s.git.rememberSwitch.On().Set(false)
	if len(calls) != 3 || calls[2].store {
		t.Fatalf("turning remember off = %+v, want a store:false drop", calls)
	}
}

// An empty token never stores, even with the switch on: there is nothing to
// remember, so the hook is asked to drop rather than persist "".
func TestGitRememberEmptyTokenDrops(t *testing.T) {
	s, _, _ := withGit(t)
	s.git.ensureWidgets()
	var last struct {
		store bool
		token string
		hit   bool
	}
	s.SetGitRememberHook(func(store bool, token string) {
		last.store, last.token, last.hit = store, token, true
	})
	s.git.rememberSwitch.On().Set(true) // no token yet
	if !last.hit || last.store {
		t.Fatalf("remember on with empty token = %+v, want store:false", last)
	}
}

// SetGitRemember reflects host state into the switch WITHOUT re-triggering the
// hook — the startup-restore path, where the token was just fed back in and we
// must not bounce it straight back to the credential store.
func TestSetGitRememberIsQuiet(t *testing.T) {
	s, _, _ := withGit(t)
	s.git.ensureWidgets()
	fired := false
	s.SetGitRememberHook(func(bool, string) { fired = true })

	s.SetGitToken("ghp_restored")
	s.SetGitRemember(true)
	if fired {
		t.Fatal("SetGitRemember must not fire the hook (startup restore path)")
	}
	if !s.git.remember.Get() || !s.git.rememberSwitch.On().Get() {
		t.Fatal("SetGitRemember(true) must reflect as on in both the field and the switch")
	}
}
