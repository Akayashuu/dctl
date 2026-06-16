package dctl

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Handler runs an interaction and returns the reply.
type Handler func(ctx context.Context, ix Interaction) (Response, error)

type regEntry struct {
	cmd     *Command
	handler Handler
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
	if _, ok := r.entries[name]; !ok {
		r.order = append(r.order, name)
	}
	r.entries[name] = regEntry{cmd: cmd, handler: h}
	return r
}

// Update is an alias for Add, for call sites that mean "replace".
func (r *Registry) Update(cmd *Command, h Handler) *Registry { return r.Add(cmd, h) }

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

// Sync reconciles Discord with the registry: it creates new commands, edits
// existing ones, and deletes those no longer bound.
func (r *Registry) Sync(ctx context.Context) error {
	base, err := r.in.commandsBase(ctx)
	if err != nil {
		return err
	}
	var existing []RegisteredCommand
	if err := r.in.rt.Do(ctx, http.MethodGet, base, nil, &existing); err != nil {
		return err
	}
	byName := make(map[string]RegisteredCommand, len(existing))
	for _, c := range existing {
		byName[c.Name] = c
	}

	desired := r.Commands()
	keep := make(map[string]bool, len(desired))
	for _, cmd := range desired {
		keep[cmd.Name()] = true
		if cur, ok := byName[cmd.Name()]; ok {
			if err := r.in.rt.Do(ctx, http.MethodPatch, base+"/"+seg(cur.ID), cmd.build(), nil); err != nil {
				return err
			}
			continue
		}
		if err := r.in.rt.Do(ctx, http.MethodPost, base, cmd.build(), nil); err != nil {
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

// Dispatch routes an interaction to its handler by command name.
func (r *Registry) Dispatch(ctx context.Context, ix Interaction) (Response, error) {
	r.mu.Lock()
	e, ok := r.entries[ix.Data.Name]
	r.mu.Unlock()
	if !ok {
		return Response{}, fmt.Errorf("dctl: unknown command %q", ix.Data.Name)
	}
	return e.handler(ctx, ix)
}
