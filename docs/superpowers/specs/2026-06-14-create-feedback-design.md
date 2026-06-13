# Spec 1/4 — Feedback de création enrichi (`/session create`)

Date: 2026-06-14
Statut: design (aucune implémentation)

## 1. Objectif

Quand un utilisateur lance `/session create`, le retour doit dire **exactement où le
travail va se passer** au lieu d'un simple « running on <#…> ». Deux surfaces :

1. **Réponse éphémère** à l'interaction (visible par l'auteur uniquement).
2. **Message posté dans le salon/forum créé** (visible par tous les participants),
   qui sert de banner de contexte pour la session.

Les deux doivent exposer : projet/repo, chemin du worktree, branche `session/<name>`,
mode (worktree isolé vs shared), et la commande de bridge lancée.

## 2. Comportement actuel vs souhaité

### Actuel (`handler.go` `sessionCreate`, l.118-177)
- Construit `worktree`/`note` (note = `" (shared — not a git repo)"` uniquement).
- Crée le salon (category → texte) ou le post forum.
- Pour le forum, poste `"Session **<name>** started."` comme message initial.
- Pour la category, **aucun message n'est posté dans le salon**.
- Réponse: `✅ Session **<name>** running on <#<id>><note>.` — pas de repo, pas de
  chemin worktree, pas de branche, pas de commande.

### Souhaité
- La réponse éphémère ET le message in-channel listent repo / mode / worktree /
  branche / commande.
- Le banner in-channel est posté dans **les deux** cas (category et forum), avec le
  même corps, pour homogénéité.

## 3. Données nécessaires

| Donnée   | Source                                                              |
|----------|--------------------------------------------------------------------|
| repo     | `st.Repo` (défaut = cwd du daemon). **Non passé au Handler aujourd'hui.** |
| worktree | `path` renvoyé par `wt.Create(name)` (abs path ; `""` = shared)     |
| branche  | `"session/" + name` (constante, seulement si worktree non vide)     |
| mode     | dérivé : worktree non vide → isolé ; sinon shared                   |
| cmd      | variable `cmd` déjà calculée (`defaultCmd` ou opt `cmd`)            |
| salon    | `sess.ChannelID`, `home.Type` ("category"→texte | "forum")          |

`repo` n'est pas accessible depuis `Handler`. Choix retenu : injecter le repo dans
`Handler` (champ `repo string` + paramètre de `NewHandler`), renseigné depuis
`serve.go` avec la même valeur que celle passée à `NewWorktreer` (la var `repo`
résolue l.60-63). Affichage : `filepath.Base(repo)` comme nom de projet, chemin
complet en `code span`.

## 4. Format exact des messages

Helper unique `func sessionBanner(repo, name, worktree, cmd string) string` (pkg
handler) produisant le corps partagé :

**Mode worktree isolé** (`worktree != ""`) :
```
🚀 Session **<name>** ready.
• Project: **<base(repo)>** (`<repo>`)
• Mode: isolated worktree
• Worktree: `<worktree>`
• Branch: `session/<name>`
• Command: `<cmd>`
```

**Mode shared, dans un repo git** (`shared:true` demandé, worktree vide) :
```
🚀 Session **<name>** ready.
• Project: **<base(repo)>** (`<repo>`)
• Mode: shared (main checkout)
• Branch: — (runs on current branch)
• Command: `<cmd>`
```

**Mode shared, repo non-git** (fallback, `wt.Create` a renvoyé `""`) :
```
🚀 Session **<name>** ready.
• Project: **<base(repo)>** (`<repo>`)
• Mode: shared (not a git repo)
• Command: `<cmd>`
```

Règle de sélection :
- `worktree != ""` → isolé.
- `worktree == "" && shared` → shared (main checkout).
- `worktree == "" && !shared` → shared (not a git repo).

**Réponse éphémère** (retour de l'interaction) — préfixe court + banner :
```
✅ Running on <#<channelID>>.

<banner>
```
`Ephemeral: true` conservé.

**Message in-channel** — exactement `<banner>` (sans le préfixe « Running on »,
puisqu'on y est déjà). Posté via `h.d.Send(ctx, sess.ChannelID, banner)`.

Note : pour le forum, le `ForumPost` initial reste obligatoire (un post forum exige
un message de départ). Deux options ; **retenue : option A** pour rester homogène.
- **Option A (retenue)** : garder `ForumPost(..., "Session **<name>** starting…")`
  comme amorce, puis poster le banner avec `Send` comme tout salon.
- Option B : passer directement le banner comme contenu du `ForumPost`. Rejetée car
  le banner doit être calculé après création (il ne dépend pas du salon ici, mais on
  préfère un seul chemin de post pour les deux types).

## 5. Fichiers / fonctions à toucher

1. **`internal/handler/handler.go`**
   - `discord` interface : ajouter `Send(ctx context.Context, channelID, content string) (*dctl.Message, error)` (déjà fourni par `*dctl.Client`, cf. `dctl.go:91`).
   - `Handler` struct : ajouter champ `repo string`.
   - `NewHandler` : ajouter param `repo string`.
   - `sessionCreate` : après `sup.Start`, calculer `banner := sessionBanner(...)`,
     poster `h.d.Send(ctx, sess.ChannelID, banner)` (best-effort, voir cas limites),
     renvoyer la réponse éphémère enrichie. Supprimer la var `note`.
   - Nouveau helper `sessionBanner` (+ import `path/filepath`).

2. **`internal/serve/serve.go`** (l.64) : `handler.NewHandler(c, sup, wt, st, repo, o.DefaultCmd)`.

3. **Mocks de test** du package handler : ajouter `Send` au fake `discord`.

Pas de changement dans `state.go` ni `worktree.go`.

## 6. Cas limites

- **repo non-git** → `wt.Create` renvoie `("", nil)`, mode « not a git repo ». Le
  banner n'affiche ni worktree ni branche.
- **`shared:true` dans un repo git** → on ne crée pas de worktree ; mode « shared
  (main checkout) ». Bien distinguer du cas non-git.
- **Échec du `Send` in-channel** : ne PAS échouer la création (session déjà
  persistée + bridge démarré). Best-effort : `_ = h.d.Send(...)`. La réponse
  éphémère reste la source de vérité.
- **Rollback** : les chemins d'erreur existants (create channel/forum, persist,
  start bridge) restent inchangés ; le `Send` se fait après, donc hors rollback.
- **Forum** : le message d'amorce `ForumPost` + le banner `Send` font deux messages.
  Acceptable (amorce courte). Ne pas archiver/altérer l'ordre.
- **Longueur** : banner < 2000 chars (limite Discord) tant que repo/worktree/cmd
  sont raisonnables ; aucun garde-fou requis vu les contraintes de `sessionNameRe`.
- **Chemins avec espaces** : déjà entourés de backticks, pas d'échappement requis.

## 7. Critères de succès / tests

Tests table-driven dans `internal/handler` (fake `discord` enregistrant les appels
`Send`) :

1. **category + worktree isolé** : la réponse éphémère contient `Project:`,
   `Worktree: ` + le path, `Branch: \`session/<name>\``, `Command:`, et
   `Mode: isolated worktree`. Un `Send` a été émis sur `sess.ChannelID` avec le même
   banner.
2. **category + shared (non-git)** : réponse contient `Mode: shared (not a git repo)`,
   PAS de `Worktree:`, PAS de `Branch:`. Un `Send` émis.
3. **category + shared:true (repo git)** : `Mode: shared (main checkout)`, PAS de
   `Worktree:`, présence de la ligne `Branch: — (runs on current branch)`.
4. **forum + worktree isolé** : `ForumPost` reçoit l'amorce ; un `Send` séparé porte
   le banner ; réponse éphémère enrichie.
5. **Send échoue** : `sessionCreate` renvoie quand même la réponse `✅`/`🚀`
   (succès), la session reste persistée.
6. **repo affiché** = `filepath.Base(st.Repo)` et le path complet apparaît.

Succès = tous verts + `go build ./...` OK + revue visuelle d'un message rendu.
