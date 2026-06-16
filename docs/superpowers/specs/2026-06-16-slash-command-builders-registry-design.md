# dctl — builders de slash commands + registry nom→handler

**Date :** 2026-06-16
**Statut :** design validé, prêt pour implémentation

## Contexte

Aujourd'hui `Interactions.RegisterCommands(ctx, []map[string]any)` fait un seul
`PUT` bulk et les commandes sont écrites à la main en `map[string]any`. On veut :

- des **builders typés** couvrant l'intégralité de l'API command Discord (tous les
  types d'options, choices, ranges, channel types, autocomplete, NSFW,
  permissions, et la **localisation** complète) ;
- que **dctl gère l'enregistrement** (add / remove / update) via un *diff* réel
  contre Discord, pas juste un bulk overwrite ;
- un **Registry** qui tient `nom → handler`, **dispatch** l'interaction entrante
  vers le bon handler, et se **synchronise** vers Discord. Le gateway consommateur
  n'a plus qu'à builder une commande + binder une fonction.

dctl reste un **package pur** (stdlib + `internal/transport` seulement) ;
`purity_test.go` doit rester vert.

## Fichiers

```
locales.go     type Locale + constantes (toutes les locales Discord)
commands.go    builders Command / Option / Choice
registry.go    type Registry (Add/Remove/Update/Sync/Dispatch) + Handler
interactions.go  + List / Create / Edit / Delete / Register (ops granulaires)
```

## locales.go — objet Language

```go
type Locale string
const (
    LocaleEnUS Locale = "en-US"
    LocaleFR   Locale = "fr"
    // … les 30+ locales supportées par Discord
)
```

Utilisé partout où Discord accepte `name_localizations` /
`description_localizations` : commande, option, choice.

## commands.go — builders

Type de commande via constructeurs : `NewCommand` (CHAT_INPUT),
`NewUserCommand`, `NewMessageCommand`.

```go
cmd := dctl.NewCommand("set", "dctl settings").
    Loc(dctl.LocaleFR, "config", "réglages dctl").
    Perms(dctl.PermManageGuild).
    DMPermission(false).
    With(
        dctl.Sub("home", "Set the category", dctl.Channel("channel", "Category", true)),
        dctl.Sub("workspace", "Set the workspace root",
            dctl.String("path", "Absolute path", true).Len(1, 4000).Loc(dctl.LocaleFR, "chemin", "Chemin absolu")),
    )
```

- `*Command` : `.Loc`, `.Perms`, `.DMPermission`, `.NSFW`, `.With(opts...)`.
- Constructeurs d'options libres renvoyant une valeur `Option` fluent :
  `String/Int/Bool/User/Channel/Role/Mentionable/Number/Attachment`,
  plus `Sub(name, desc, opts...)` (SUB_COMMAND) et `Group(name, desc, subs...)`
  (SUB_COMMAND_GROUP).
- Méthodes `Option` : `.Loc`, `.Range(min,max)` (min/max value), `.Len(min,max)`
  (min/max length), `.Choices(...)`, `.ChannelTypes(...)`, `.Autocomplete()`.
- `Choice(name, value).Loc(locale, name)`.
- Codes de type Discord (commande 1–3, option 1–11) privés, jamais exposés.
- `(*Command).build() map[string]any` produit la forme wire ; `JSON()` l'expose.

## interactions.go — ops granulaires

Helper interne `commandsBase(ctx)` → `/applications/{app}/guilds/{guild}/commands`.

```go
func (in *Interactions) List(ctx) ([]RegisteredCommand, error)        // GET
func (in *Interactions) Create(ctx, *Command) (RegisteredCommand, error) // POST
func (in *Interactions) Edit(ctx, id string, *Command) error          // PATCH .../{id}
func (in *Interactions) Delete(ctx, id string) error                  // DELETE .../{id}
func (in *Interactions) Register(ctx, ...*Command) error              // PUT bulk
```

`RegisterCommands([]map[string]any)` conservé (back-compat).
`RegisteredCommand{ID, Name, Description}` = forme de lecture.

## registry.go — registry + dispatch

```go
type Handler func(ctx context.Context, ix Interaction) (Response, error)

reg := client.Interactions().Registry()
reg.Add(cmd, handler)         // upsert nom→{définition, handler}
reg.Update(cmd2, handler2)    // alias d'upsert (clarté d'intention)
reg.Remove("ping")

reg.Sync(ctx)                 // diff : List existantes → Create / Edit / Delete
resp, err := reg.Dispatch(ctx, ix)  // route par ix.Data.Name vers le handler
```

- Le Registry garde l'ordre d'insertion et une map `nom → {cmd, handler}`,
  protégés par `sync.Mutex` (bind concurrent possible).
- `Sync` : `List` les commandes guild, puis pour chaque désirée absente → `Create`,
  présente → `Edit` (par id), et chaque existante non-désirée → `Delete`. C'est le
  vrai add/remove/update.
- `Dispatch` : lookup par nom ; inconnu → erreur `dctl: unknown command %q`.

## Testing

Tout sur le stub `transport.Doer` (`Reply` pour `List`, `Last/Calls` pour vérifier
method/path/body). Couvre : chaque type d'option, localisation, ranges/len/choices,
le diff de `Sync` (create+edit+delete dans un même cycle), `Dispatch` (hit + miss).
`purity_test.go` reste vert.

## Non-objectifs

- Pas de commandes globales (guild-scoped only, comme l'existant).
- Pas de validation côté client des contraintes Discord (longueurs max, 25 choices
  max sauf là où déjà fait) au-delà du raisonnable — Discord reste l'autorité.
