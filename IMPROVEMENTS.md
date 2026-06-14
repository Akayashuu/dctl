# Pistes d'amélioration — dctl

État du projet : CLI/bot Discord en Go (~11k lignes, 272 tests qui passent).
Le cœur est solide. Ci-dessous les axes par priorité.

---

## Priorité haute

### 1. Aucune CI ni linting
Pas de `.github/workflows`, pas de `.golangci.yml`, pas de `Makefile`.
272 tests existent mais rien ne les exécute automatiquement → une régression
sur une branche passe inaperçue.

- [ ] Workflow GitHub Actions : `go test ./...`
- [ ] `golangci-lint` + `.golangci.yml`
- [ ] `go build ./cmd/dctl`
- [ ] Déclenché sur PR et push master

### 2. Code cross-OS sous-testé
`internal/service/` (systemd/launchd/Task Scheduler, ~676 lignes) n'a que des
tests de génération de fichiers, jamais un cycle install → uninstall réel.
Code à haut risque qui touche le système.

- [ ] Tests d'intégration install/uninstall (ou mocks systemctl/launchctl)
- [ ] Vérifier le nettoyage : env file, unit file, binaire

---

## Priorité moyenne

### 3. Nettoyage des worktrees non testé
`internal/worktree/` teste la création mais pas :
- [ ] close sur worktree sale (dirty)
- [ ] sessions à moitié démarrées (stale)
- [ ] orphelins de branche / worktree au crash du daemon

### 4. Robustesse du backend tmux
Les commits récents (détection de choix par tour, frames busy, escaping
send-keys) montrent une machine à états sensible aux cas limites.

- [ ] Property-based testing sur l'extraction de tour
- [ ] Property-based testing sur la détection des prompts de choix

### 5. `handler_test.go` (1171 lignes)
- [ ] Scinder par famille de commandes pour la lisibilité

---

## Priorité basse

- [ ] Logging structuré (`slog`) pour l'observabilité du daemon, au lieu de stderr brut
- [ ] Schéma de dépendances des modules
- [ ] Runbook de dépannage (tmux qui hang, worktrees périmés, erreurs de permission)

---

## Ordre conseillé
1. **CI** — effort faible, gros filet de sécurité.
2. **Tests worktree + service** — le code le plus fragile et le moins couvert.
3. Le reste au fil de l'eau.
