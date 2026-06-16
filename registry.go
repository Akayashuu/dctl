package dctl

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Handler runs a command interaction and returns the reply.
type Handler func(ctx context.Context, ix Interaction) (Response, error)

// AutocompleteHandler returns the suggestions for an autocomplete interaction.
type AutocompleteHandler func(ctx context.Context, ix Interaction) ([]AutocompleteChoice, error)

type regEntry struct {
	cmd          *Command
	handler      Handler
	autocomplete AutocompleteHandler
}

// Registry holds name→{command, handler} bindings, syncs them to Discord
// (add/remove/update) and dispatches incoming interactions to their handler.
type Registry struct {
	in      *Interactions
	mu      sync.Mutex
	order   []string
	entries map[string]regEntry
}

// Add binds a command to a handler, replacing any existing binding by name.
func (r *Registry) Add(cmd *Command, h Handler) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := cmd.Name()
	e := r.entries[name]
	if e.cmd == nil {
		r.order = append(r.order, name)
	}
	e.cmd, e.handler = cmd, h
	r.entries[name] = e
	return r
}

// Update is an alias for Add, for call sites that mean "replace".
func (r *Registry) Update(cmd *Command, h Handler) *Registry { return r.Add(cmd, h) }

// Autocomplete attaches an autocomplete handler to an already-added command.
func (r *Registry) Autocomplete(name string, h AutocompleteHandler) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[name]; ok {
		e.autocomplete = h
		r.entries[name] = e
	}
	return r
}

// Remove drops a binding by command name.
func (r *Registry) Remove(name string) *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return r
}

// Commands returns the bound commands in insertion order.
func (r *Registry) Commands() []*Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Command, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.entries[n].cmd)
	}
	return out
}

type desiredCmd struct {
	name string
	body map[string]any
}

// snapshot builds every desired command's wire body under the lock, so the
// reconcile loop never reads a command being mutated concurrently.
func (r *Registry) snapshot() []desiredCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]desiredCmd, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, desiredCmd{name: n, body: r.entries[n].cmd.build()})
	}
	return out
}

// Sync reconciles Discord with the registry: it creates new commands, edits
// existing ones, and deletes those no longer bound. It refuses to run on an
// empty registry while commands exist, to avoid silently deleting everything.
func (r *Registry) Sync(ctx context.Context) error {
	base, err := r.in.commandsBase(ctx)
	if err != nil {
		return err
	}
	var existing []RegisteredCommand
	if err := r.in.rt.Do(ctx, http.MethodGet, base, nil, &existing); err != nil {
		return err
	}

	desired := r.snapshot()
	if len(desired) == 0 && len(existing) > 0 {
		return fmt.Errorf("dctl: Sync on empty registry would delete %d command(s); add commands first", len(existing))
	}

	byName := make(map[string]RegisteredCommand, len(existing))
	for _, c := range existing {
		byName[c.Name] = c
	}
	keep := make(map[string]bool, len(desired))
	for _, d := range desired {
		keep[d.name] = true
		if cur, ok := byName[d.name]; ok {
			if err := r.in.rt.Do(ctx, http.MethodPatch, base+"/"+seg(cur.ID), d.body, nil); err != nil {
				return err
			}
			continue
		}
		if err := r.in.rt.Do(ctx, http.MethodPost, base, d.body, nil); err != nil {
			return err
		}
	}
	for _, c := range existing {
		if !keep[c.Name] {
			if err := r.in.rt.Do(ctx, http.MethodDelete, base+"/"+seg(c.ID), nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dispatch routes a command interaction to its handler by command name.
// Autocomplete interactions must go through DispatchAutocomplete instead.
func (r *Registry) Dispatch(ctx context.Context, ix Interaction) (Response, error) {
	if ix.Type == InteractionAutocomplete {
		return Response{}, fmt.Errorf("dctl: autocomplete interaction for %q; use DispatchAutocomplete", ix.Data.Name)
	}
	r.mu.Lock()
	e, ok := r.entries[ix.Data.Name]
	r.mu.Unlock()
	if !ok {
		return Response{}, fmt.Errorf("dctl: unknown command %q", ix.Data.Name)
	}
	return e.handler(ctx, ix)
}

// DispatchAutocomplete routes an autocomplete interaction to its autocomplete
// handler by command name.
func (r *Registry) DispatchAutocomplete(ctx context.Context, ix Interaction) ([]AutocompleteChoice, error) {
	r.mu.Lock()
	e, ok := r.entries[ix.Data.Name]
	r.mu.Unlock()
	if !ok || e.autocomplete == nil {
		return nil, fmt.Errorf("dctl: no autocomplete handler for %q", ix.Data.Name)
	}
	return e.autocomplete(ctx, ix)
}
