# Spec 4/4 — Mémoire des participants + allowlist par session

Date : 2026-06-14
Statut : design (aucun code écrit)

## 1. Objectif

Aujourd'hui dctl a une seule allowlist **globale** (`State.Allow`) : un user est
autorisé ou non à invoquer *toutes* les commandes, sur *toutes* les sessions. Les
`Session` ne savent rien des humains qui leur parlent.

On veut deux capacités liées :

1. **Mémoire des participants** — chaque session retient *avec qui elle parle*,
   c.-à-d. la liste des users dont un message entrant a été traité par son bridge.
2. **Allowlist par-session** — pouvoir autoriser explicitement plusieurs users sur
   une session précise, en plus (ou à la place) de la globale, via des commandes.

Non-objectifs (YAGNI) : rôles/permissions fines, quotas par user, historique
horodaté des messages, UI riche. On stocke des `userID` Discord, point.

## 2. Modèle de données

Extension de `state.Session` (internal/state/state.go) — deux champs `omitempty`
rétro-compatibles (les sessions JSON existantes se chargent avec slices nil) :

```go
type Session struct {
    Name         string   `json:"name"`
    ChannelID    string   `json:"channelID"`
    Type         string   `json:"type"`
    Cmd          string   `json:"cmd"`
    Worktree     string   `json:"worktree,omitempty"`
    Allow        []string `json:"allow,omitempty"`        // allowlist par-session (curatée)
    Participants []string `json:"participants,omitempty"` // observés : auteurs vus par le bridge
}
```

Distinction volontaire :
- **`Allow`** = intention explicite d'un admin (« ces gens ont le droit ici »).
- **`Participants`** = fait observé (« ces gens ont écrit ici »), alimenté
  automatiquement. Mélanger les deux ferait qu'un simple bavard s'auto-autoriserait.

### Nouvelles méthodes sur `*State` (mutex-guarded, persistées)

```go
func (s *State) AddSessionAllow(name, userID string) (bool, error)
func (s *State) RemoveSessionAllow(name, userID string) (bool, error)
func (s *State) SessionAllowed(name, userID string) bool          // global OR par-session
func (s *State) RecordParticipant(name, userID string) (bool, error) // idempotent, retourne true si nouveau
func (s *State) SessionParticipants(name string) []string
func (s *State) SessionAllowlist(name string) []string
```

Toutes opèrent par recherche `name` dans `s.Sessions` (comme `FindSession`) et
appellent `saveLocked()`. `RecordParticipant` ne persiste que si l'ID est nouveau
(évite une écriture disque à chaque message).

## 3. Sémantique d'autorisation

Règle retenue : **global OR par-session** (union, pas remplacement).

```
autorisé(user, session) := State.Allowed(user) || session.Allow contient user
```

- La globale reste l'allowlist « admin » (qui peut créer/fermer des sessions,
  gérer le home, etc.).
- L'allowlist par-session n'élargit l'accès **que** sur le salon de cette session.

Conséquence importante sur le point d'application : `Handler.Handle` traite des
**slash-commands** (`/session`, `/allow`, `/set`) — ce sont des actions de gestion
qui doivent rester gardées par la **globale**. L'allowlist par-session ne concerne
PAS les slash-commands ; elle concerne **qui peut faire avancer le bridge en
écrivant dans le salon de la session**.

Or — point d'architecture clé — le bridge (`internal/bridge/bridge.go`) tourne dans
un **process enfant séparé** lancé par le supervisor (`dctl bridge -c <chan>
--cmd ...`). Il ne partage pas le `*state.State` en mémoire du daemon et, à ce
jour, n'applique aucun filtre d'auteur : il répond à tout humain non-bot du salon.

Trois traitements possibles de l'allowlist par-session côté bridge :
- **(A)** Le bridge ignore l'allowlist et répond à tout le monde dans son salon.
  L'allowlist par-session est alors purement déclarative/documentaire. Plus simple,
  mais le mot « allow » ne fait rien → trompeur.
- **(B)** Le bridge applique l'allowlist : il ne traite que les messages dont
  `m.Author.ID` est globalement autorisé OU dans `session.Allow`. Nécessite que le
  process bridge connaisse l'allowlist (cf. §6, dépendance Spec 3).
- **(C)** Hybride : le bridge accepte tout le monde par défaut, mais un flag
  `--allow-file`/`--allow` optionnel active le filtrage quand il est fourni.

Recommandation : **(B)** comme sémantique cible, livrée en deux temps si besoin —
d'abord l'enregistrement participants + commandes (déclaratif), puis l'application
côté bridge. Voir §8.

## 4. Nouvelles commandes (slash)

Toutes restent gardées par `h.st.Allowed(...)` (globale) au sommet de `Handle`.
Sous-groupe sous `/session` pour ne pas confondre avec `/allow` global :

```
/session allow add    name:<session> user:<@user>   → AddSessionAllow
/session allow remove name:<session> user:<@user>   → RemoveSessionAllow
/session allow list   name:<session>                → SessionAllowlist
/session who          name:<session>                → SessionParticipants (observés)
```

- `who` répond en ephemeral avec la liste des participants observés, formatés
  `<@id>`. Vide → « Personne n'a encore écrit dans cette session. »
- `allow list` affiche la curatée + rappelle que la globale s'ajoute.
- Validation `name` via `sessionNameRe` existant ; erreur `no session %q` si absente
  (réutiliser `FindSession`).

Alternative écartée (YAGNI) : un `/session allow set` qui remplace toute la liste —
pas demandé, `add`/`remove` suffisent.

## 5. Où / quand on enregistre les participants

L'auteur d'un message entrant est déjà disponible dans la boucle bridge
(`bridge.go`, `m.Author.Username` / `m.Author.ID`). C'est le seul endroit où passe
le flux humain → session.

Point d'enregistrement : dans `Run`, juste après le filtre `if m.Author.Bot {
continue }` et avant/après le traitement, appeler un enregistrement par
`m.Author.ID` sur la session courante. Idempotent (ne persiste que les nouveaux).

Problème de frontière de process (voir §3) : le bridge est un binaire enfant et
**n'a pas** le `*State` du daemon. Trois options pour faire remonter le participant :

- **(P1) Le bridge écrit dans le state lui-même.** On passe `--state-file <state.json>`
  et `--session <name>` au `dctl bridge`, et le bridge ouvre/charge/écrit l'état
  partagé. ⚠️ Course d'écriture avec le daemon (les deux process écrivent le même
  fichier via `saveLocked` rename atomique). Nécessite verrou inter-process →
  dépend de **Spec 3 (isolation multi-instance)** qui définit le verrouillage/le
  fichier d'état par instance. À ne pas faire avant Spec 3.
- **(P2) Le bridge écrit un journal append-only séparé** (`participants/<name>.log`,
  un userID par ligne) ; le daemon le lit à la demande pour `/session who`. Pas de
  course (append O_APPEND), pas de partage du `*State`. Simple, robuste, découplé.
- **(P3) Le bridge notifie le daemon** via un canal IPC (socket/HTTP local). Le
  daemon reste seul propriétaire du state. Le plus propre, mais introduit un
  transport qui n'existe pas encore → trop lourd pour le besoin.

Recommandation : **(P2)** pour l'enregistrement (découplé, sans dépendre de Spec 3),
en exposant les participants au daemon via lecture du journal au moment du `who`.
Si Spec 3 atterrit avec un verrou d'état partagé propre, on pourra migrer vers (P1)
et stocker `Participants` directement dans `Session`.

Note : `Session.Participants` (champ JSON du §2) reste utile comme cache/forme
canonique si on adopte (P1) plus tard ; avec (P2) c'est le journal qui fait foi et
`SessionParticipants` lit le journal.

## 6. Fichiers à toucher

- `internal/state/state.go` — champs `Allow`/`Participants` sur `Session` +
  méthodes §2. (Cœur, indispensable.)
- `internal/handler/handler.go` — router `/session allow add|remove|list` et
  `/session who` dans `handleSession`; helpers de réponse.
- Définition des slash-commands (là où `/session`/`/allow` sont déclarés auprès de
  Discord — chercher l'enregistrement des application commands, hors fichiers lus
  ici) — ajouter les sous-commandes + options `name`/`user`.
- `internal/bridge/bridge.go` — enregistrement participant dans la boucle (§5) ;
  si sémantique (B) retenue, filtre d'auteur avant `resp.Respond`.
- `internal/supervisor/supervisor.go` — passer les nouveaux flags au `dctl bridge`
  (`--session <name>` et, selon §5/§3, `--state-file` ou chemin du journal).
- `cmd/dctl` (parsing des flags du sous-commande `bridge`) — accueillir ces flags
  dans `bridge.Options`.

## 7. Cas limites

- **Session absente** lors d'un `/session allow|who` → erreur claire, pas de panic.
- **Double add / remove inexistant** → idempotent, message neutre (« déjà présent »
  / « pas dans la liste »).
- **Bot/lui-même** : jamais enregistré (le filtre `m.Author.Bot` existe déjà).
- **Session fermée** (`RemoveSession`) → purger aussi le journal participants (P2)
  ou les champs (P1) ; sinon fuite de fichiers `participants/*.log`.
- **Migration** : anciens `state.json` sans `allow`/`participants` → slices nil,
  fonctionnent (zero-value). Aucun script de migration requis.
- **Course d'écriture state** (si P1 sans Spec 3) → corruption/perte : raison de
  préférer (P2) tant que Spec 3 n'est pas là.
- **User retiré de l'allowlist par-session** mais déjà dans `Participants` →
  normal : participant = fait observé, pas un droit ; il reste listé par `who`.
- **Mention vs ID** : les options Discord `user:` fournissent un ID ; normaliser
  (strip `<@ >`) si l'option est de type string et non `USER`.

## 8. Plan de livraison recommandé (incrémental)

1. Champs `Session.Allow`/`Participants` + méthodes state (§2).
2. Commandes `/session allow add|remove|list` + `/session who` (déclaratif).
3. Enregistrement participants côté bridge via journal append-only (P2) + flag
   `--session`/journal au supervisor.
4. (Optionnel / après Spec 3) Application de l'allowlist côté bridge (sémantique B)
   et/ou migration du stockage participants vers `Session` (P1) sous verrou partagé.

## 9. Critères de succès

- `/session allow add` puis `/session allow list` montrent le user ; `remove` le
  retire ; persistance survit à un redémarrage du daemon.
- Après que deux humains distincts ont écrit dans le salon d'une session,
  `/session who` les liste tous les deux (et personne d'autre, pas les bots).
- Les sessions/state JSON pré-existants se chargent sans erreur (champs nil).
- Aucune régression : la globale garde encore toutes les slash-commands de gestion.
- (Si sémantique B livrée) un user ni global ni dans `session.Allow` qui écrit dans
  le salon ne déclenche aucune exécution de commande.
- Pas de course d'écriture sur `state.json` (P2) ou verrou en place (P1+Spec 3).

## 10. Dépendance Spec 3

L'enregistrement participants depuis le process bridge touche au même fichier d'état
que le daemon. **Spec 3 (isolation multi-instance)** définit le découpage/verrou de
l'état par instance ; tant qu'il n'est pas livré, on évite l'écriture concurrente en
passant par le journal append-only (P2). Une fois Spec 3 en place, (P1) devient sûr
et l'on peut consolider les participants dans `Session`.
