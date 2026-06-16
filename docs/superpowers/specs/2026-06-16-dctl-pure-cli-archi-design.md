# dctl — refonte archi : CLI Discord pur, CRUD complet

**Date :** 2026-06-16
**Statut :** design validé, prêt pour plan d'implémentation

## Contexte

dctl est aujourd'hui une librairie Go (`package dctl`) : un client REST fin pour
l'API Discord v10. Toutes les méthodes pendent d'un seul `Client` (god-object,
~25 méthodes réparties par fichier), chacune répétant le même boilerplate
`Enabled()` → `newRequest` → `do`. La doc du package le présente encore comme
support de deux consommateurs (CLI + bridge prospector / agent IA).

Objectif : faire de dctl **un CLI Discord pur et autonome**, avec une archi qui
scale à la couverture CRUD complète de l'API Discord, sans god-object ni
copier-coller, et sans baggage d'écosystème dans la doc.

Contrainte de compat : **big-bang, API publique libre** — on peut casser/renommer
toute la surface publique. Pas de consommateur à préserver.

## Périmètre fonctionnel — CRUD Discord complet

Couverture cible, une ressource = un sous-client :

| Ressource | Opérations cibles |
|---|---|
| **Channels** | create (texte/forum/catégorie), list, get, type, rename/update, move, archive, delete |
| **Roles** | create, list, get, update (nom/couleur/permissions), delete, assign/remove sur un membre |
| **Messages** | send, reply, read (historique), edit, delete |
| **Members** | list, get, kick, ban, manage roles |
| **Reactions** | add, remove |
| **Threads / Forums** | start thread, create forum, forum post, archive |
| **Permissions** | channel permission overwrites (set/remove) |
| **Webhooks** | create, list, delete, execute |
| **Interactions** (existant) | register commands, respond, defer, autocomplete, edit response |
| **Components** (existant) | select menu, ack |
| **Guilds** (support) | list, sole guild, resolve |

Les ressources existantes (interactions, components, reactions, threads partiels)
sont conservées, juste reclassées dans la nouvelle structure. Le reste est ajouté.

## Architecture — 3 couches, un seul package public

```
dctl/
  dctl.go              façade Client + New() + accesseurs sous-clients
  internal/
    transport/         cœur HTTP : auth, Do(), erreurs, (futur rate-limit/retry)
  types.go             DTO partagés (Message, Channel, Guild, Author, Attachment…)
  guilds.go            type Guilds + defaults/resolvers partagés
  channels.go          type Channels
  messages.go          type Messages
  roles.go             type Roles
  members.go           type Members
  reactions.go         type Reactions
  threads.go           type Threads
  permissions.go       type Permissions
  webhooks.go          type Webhooks
  interactions.go      type Interactions
  components.go        type Components
```

**Un seul package public `dctl`** (import ergonomique `dctl.New(...)`, pas de
sprawl d'import paths). Les ressources sont des sous-clients exposés par
accesseurs ; seul le transport est dans `internal/`.

### Couche 1 — Transport (le seul vrai port)

Interface unique, unique chose mockable :

```go
// internal/transport
type Doer interface {
    Do(ctx context.Context, method, path string, body, out any) error
}
```

L'implémentation réelle absorbe **tout** le boilerplate actuel :
- check token configuré → `ErrDisabled`
- construction requête (`APIBase+path`), headers `Authorization: Bot <token>`,
  User-Agent, Content-Type
- exécution, lecture limitée (1 MiB), parsing de l'erreur Discord
  (`discord <code>: <body>`), décodage JSON de la réponse

C'est le **point d'accroche unique** pour les évolutions futures (rate-limit,
retry/backoff, pagination). Le `if !c.Enabled()` aujourd'hui dispersé dans chaque
méthode vit désormais ici, une seule fois.

### Couche 2 — Sous-clients par ressource

Chaque ressource = une petite struct tenant le `Doer` + les défauts partagés :

```go
type defaults struct {
    channel string // channel par défaut
    guilds  *Guilds // pour resolveGuild / SoleGuild
}

type Messages struct {
    rt  transport.Doer
    def *defaults
}

func (m *Messages) Send(ctx context.Context, channelID, content string) (*Message, error)
func (m *Messages) Reply(ctx context.Context, channelID, messageID, content string) (*Message, error)
func (m *Messages) Read(ctx context.Context, channelID string, limit int, after string) ([]Message, error)
func (m *Messages) Edit(ctx context.Context, channelID, messageID, content string) (*Message, error)
func (m *Messages) Delete(ctx context.Context, channelID, messageID string) error
```

`resolveChannel` / `resolveGuild` deviennent des helpers sur `defaults` partagé,
injecté — plus dupliqués.

### Couche 3 — Façade `Client`

```go
client := dctl.New(token, defaultChannel)
client.Messages().Send(ctx, "", "salut")
client.Channels().Ensure(ctx, "", "logs")
client.Roles().Create(ctx, "", "moderator")
client.Members().AddRole(ctx, memberID, roleID)
```

`Client` ne porte plus aucune logique métier : il construit le transport, le
défaut, et expose les accesseurs (méthodes, pas champs — plus souple : lazy,
peut retourner interface plus tard). Il scale à toute l'API sans grossir.

**Accesseurs retenus :** `c.Channels()`, `c.Messages()`, `c.Roles()`,
`c.Members()`, `c.Reactions()`, `c.Threads()`, `c.Permissions()`,
`c.Webhooks()`, `c.Interactions()`, `c.Components()`, `c.Guilds()`.

### Renommages (big-bang)

- `c.Send` → `c.Messages().Send`
- `c.Reply` → `c.Messages().Reply`
- `c.Read` → `c.Messages().Read`
- `c.LastMessageAt` → `c.Messages().LastMessageAt`
- `c.Channels(ctx, guildID)` (qui **liste**) → `c.Channels().List(ctx, guildID)`
- `c.CreateChannel` → `c.Channels().Create`, etc.
- `c.React`/`c.Unreact` → `c.Reactions().Add`/`Remove`
- … idem pour toutes les ressources existantes.

La collision actuelle nom-de-méthode / nom-de-ressource (`Channels`) se résout
naturellement.

## Purification — dctl pur

### A. Doc & commentaires

Retrait de tout le vocabulaire d'écosystème :
- **Package doc** : « powers both the `dctl` CLI and the prospector backend's
  Discord bridge — one library, two consumers » → dctl = client Discord, point.
  Retirer aussi « AI agent », « best-effort notification fan-out ».
- `LastMessageAt` : retirer « inactivity signal for `session clean` » → juste
  « timestamp du dernier message du channel ».
- `DeferInteraction` / `EditInteractionResponse` : retirer « daemon »,
  « slow clones », « Claude/tool output ».
- `UpsertStatusMessage` : reformuler sans le contexte daemon.

### B. Suppression du garde-fou `noMentions`

Retrait de la variable `noMentions` (`allowed_mentions: {parse: []}`) et de son
injection automatique dans `post`, `RespondInteraction`, `DeferInteraction`,
`EditInteractionResponse`, `UpsertStatusMessage`.

**Conséquence comportementale actée :** sans ce garde-fou, les messages sortants
reprennent le comportement Discord par défaut — les `@everyone`, `@here` et
`<@id>` présents dans le contenu **pingueront réellement**. Aucun impact sur la
capacité d'envoyer un message ; seule la notification change. Choix : suppression
nette, pas de flag opt-in (YAGNI — réintroductible plus tard via une option par
message si le besoin apparaît).

## Testing

Gain principal : chaque sous-client se teste contre un **`Doer` factice** (stub
en mémoire enregistrant method/path/body, renvoyant un JSON canné) — zéro
`httptest`, zéro réseau dans les tests de ressources. Un seul jeu de tests
d'intégration vise le vrai transport (auth, headers, parsing erreur).

- Tests existants (`channels_test.go`, `components_test.go`,
  `interactions_autocomplete_test.go`, `dctl_test.go`,
  `dctl_lastmessage_test.go`) réécrits sur le stub `Doer`.
- `purity_test.go` (vérifie l'absence de dépendances externes) reste valable et
  doit continuer à passer.

## Plan de migration (une PR, big-bang)

1. Extraire `internal/transport` depuis `newRequest`/`do`/`Enabled` actuels.
2. Créer `types.go` (DTO partagés) et `defaults` (resolvers channel/guild).
3. Créer les sous-clients en **déplaçant** les méthodes existantes (la logique
   HTTP ne change pas, seulement son rangement et l'appel via `rt.Do`).
4. Ajouter les ressources manquantes : Roles, Members, Permissions, Webhooks +
   compléter Channels (update/rename/move) et Messages (edit/delete).
5. Façade `Client` + accesseurs.
6. Purification A (doc) + B (suppression `noMentions`).
7. Réécrire les tests sur le stub `Doer` ; garder `purity_test.go` vert.

## Non-objectifs (YAGNI)

- Pas de gateway/websocket (dctl reste REST on-demand).
- Pas de cache, rate-limit, retry implémentés maintenant — seulement le point
  d'accroche transport prêt à les recevoir.
- Pas de sous-packages publics importables séparément (`dctl/channels`) —
  package unique.
- Pas de flag `--no-mentions` / option opt-in pour le moment.
