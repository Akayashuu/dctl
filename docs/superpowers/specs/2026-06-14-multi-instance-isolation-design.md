# Spec 3/4 — Isolation multi-instance / multi-user

Date: 2026-06-14
Statut: Design (aucune implémentation)
Portée: socle structurant pour les autres specs (nommage, lifecycle, affichage)

## 1. Objectif

Permettre à **plusieurs instances `dctl`** (typiquement deux users distincts, chacun son
daemon `dctl serve` avec son propre `state.json` et potentiellement son propre bot)
de **partager le même home Discord** (catégorie ou forum) sans se marcher dessus.

Aujourd'hui, deux daemons qui pointent le même `HomeRef` entrent en collision sur quatre
ressources, dont trois sont **globales** (partagées hors du state local) :

| Ressource | Lieu | Namespacé aujourd'hui ? | Collision |
|---|---|---|---|
| Nom de session | `state.Session.Name`, unicité via `AddSession` | Non (local au state) | Chaque daemon ignore les sessions de l'autre → deux `foo` possibles |
| Chemin worktree | `<repo>/.dctl-sessions/<name>` | Non | `git worktree add` échoue si le path existe déjà (autre daemon, même repo) |
| Branche git | `session/<name>` | Non | `worktree add -b` échoue si la branche existe déjà |
| Channel/post Discord | créé sous `home.ID` | Non | Deux posts `foo` dans le même forum, indistinguables |

Le state local de chacun ne voit pas les sessions de l'autre : la garde d'unicité
de `AddSession` (state.go:130) est **insuffisante** pour les ressources globales.

But: garantir que pour deux instances A et B partageant un home, **aucune des quatre
ressources ci-dessus n'entre en collision**, et qu'un daemon ne touche **jamais** (close,
archive, remove worktree, restart) une session appartenant à un autre.

Non-objectif (YAGNI): coordination temps réel entre daemons, élection de leader,
state partagé en base. On vise l'isolation par construction, pas la collaboration.

## 2. Modèle d'identité

Chaque instance possède un **`instanceID`** : slug court, stable, unique par daemon.

Source (par ordre de priorité), résolue au démarrage de `serve.Run` :
1. Flag/env explicite `DCTL_INSTANCE_ID` (slug validé `^[a-z0-9][a-z0-9-]{0,15}$`).
2. À défaut, dérivé de `DCTL_OWNER_ID` (déjà présent, serve.go:48) : `u<derniers 8 chars du snowflake>` ou un hash court. L'owner id est un snowflake numérique, donc on le passe par un slugify déterministe.
3. À défaut (aucun owner, aucun id) : refus de démarrer en mode "home partagé" — on log un warning et on tombe en mode legacy non-namespacé (compat mono-instance).

L'`instanceID` est :
- **persisté dans le state** (`State.InstanceID`) à la première résolution, et **figé** ensuite (changer l'id orphelinerait toutes les ressources existantes — voir migration). Si le state porte déjà un id différent de celui résolu, on **refuse de démarrer** avec un message clair.
- court (≤16 chars) car il préfixe des noms de branche et des chemins.

Décision: on réutilise `DCTL_OWNER_ID` comme **défaut** mais on introduit `instanceID`
comme concept de premier ordre, car (a) un même user peut vouloir deux instances sur le
même home, (b) un id court est plus lisible qu'un snowflake de 18 chiffres dans les noms.

## 3. Conventions de nommage namespacées

On distingue le **nom logique** (ce que l'utilisateur tape, ex. `foo`) du **nom qualifié**
(ce qui sort sur les ressources globales, ex. `alice__foo`).

Séparateur: `__` (double underscore) — interdit dans le slug `instanceID` et déjà hors du
jeu typique des noms, donc parsable sans ambiguïté (`split("__", 2)`).

| Ressource | Aujourd'hui | Proposé |
|---|---|---|
| Branche git | `session/<name>` | `session/<instanceID>/<name>` |
| Chemin worktree | `<repo>/.dctl-sessions/<name>` | `<repo>/.dctl-sessions/<instanceID>/<name>` |
| Titre channel/post Discord | `<name>` | `<instanceID>__<name>` (ou tag `[instanceID] name` — voir §6) |
| Clé state (`Session.Name`) | nom logique | **inchangée** = nom logique (le state est déjà local à l'instance) |

Justification du choix par ressource :
- **Branche** : `session/<instanceID>/<name>` exploite la hiérarchie de refs git (`session/alice/foo`). Deux instances ne peuvent plus créer la même branche.
- **Worktree** : sous-dossier par instance → pas de collision de path, et un `git worktree prune`/inspection reste lisible par instance.
- **Discord** : préfixe dans le titre car un forum est plat ; permet à un humain de distinguer les posts de chaque owner. (Variante sous-catégorie en §5.)
- **State.Name** : reste le nom logique. La clé d'unicité locale ne change pas, donc pas de churn dans `FindSession`/`RemoveSession`. Le namespacing est appliqué **à la frontière** (worktree, git, Discord), pas dans le modèle local.

Validation: le regex `sessionNameRe` (handler.go:16) reste sur le **nom logique**.
On ajoute un regex `instanceIDRe` distinct, plus strict (lowercase, pas de `_`, ≤16).
Garantie: `instanceID` + `__` + `name` ne peut jamais réintroduire un `..` ou un `/`.

## 4. Changements par composant

### 4.1 `internal/state/state.go`
- Ajouter `InstanceID string json:"instanceID,omitempty"` à `State`.
- Helper `QualifiedName(name) string` → `InstanceID + "__" + name` (ou `name` si id vide, mode legacy).
- `AddSession` inchangé (unicité locale sur nom logique).
- Optionnel: stocker dans `Session` le `Branch` et garder `Worktree` (déjà abs) tels quels — ils encodent déjà le namespacing une fois créés, donc pas de recalcul nécessaire au restart.

### 4.2 `internal/worktree/worktree.go`
- `NewWorktreer(ctx, repo, instanceID)` : le worktreer porte l'`instanceID`.
- `path(name)` → `filepath.Join(repo, ".dctl-sessions", instanceID, name)`.
- `Create`/`Remove` → branche `session/<instanceID>/<name>`.
- Si `instanceID == ""` (legacy) : comportement actuel exact (rétrocompat).

### 4.3 `internal/handler/handler.go`
- `sessionCreate` : le nom logique reste la clé. Le titre Discord passé à `CreateChannelUnder`/`ForumPost` devient `st.QualifiedName(name)` (ou tag, §6).
- Aucun changement de la garde d'unicité locale.
- **Garde d'appartenance** (§5) appliquée dans `sessionClose` : refuser si la cible n'appartient pas à cette instance (déjà garanti car le state est local, mais voir collision résiduelle §7).

### 4.4 `internal/serve/serve.go`
- Résoudre l'`instanceID` (env → owner → state) avant de construire le worktreer.
- Persister/valider l'`instanceID` figé.
- `worktree.NewWorktreer(ctx, repo, instanceID)`.

### 4.5 `internal/supervisor/supervisor.go`
- Aucun changement requis : la clé `cancels[sess.Name]` reste le nom logique, local à l'instance. Le supervisor ne touche que les sessions de son propre state → isolation déjà acquise côté process.

## 5. Approches comparées

### Approche A — Préfixe de nommage (RETENUE)
`instanceID` préfixe branche/worktree/titre Discord ; state local sur nom logique.
- ✅ Aucune infra Discord supplémentaire ; marche sur catégorie **et** forum.
- ✅ Changements localisés (4 fichiers, pas de nouvelle dépendance).
- ✅ Isolation par construction, zéro coordination inter-daemon.
- ➖ Titres Discord un peu plus longs (`alice__foo`).

### Approche B — Sous-catégorie / sous-forum Discord par owner
Chaque instance crée (ou se voit assigner) une sous-catégorie sous le home, et y crée ses channels. Pas de préfixe de titre.
- ✅ Affichage Discord le plus propre (regroupement visuel par owner).
- ➖ Un **forum** ne peut pas contenir de sous-catégorie → ne marche pas pour `home.Type == "forum"`, qui est un cas central.
- ➖ Plus d'objets Discord à gérer, à créer/archiver, à réconcilier au restart.
- ➖ Ne résout **pas** worktree/branche (toujours besoin du préfixe pour ceux-là) → on cumule deux mécanismes.

### Approche C — Lock partagé (fichier `.dctl-lock` dans le repo / le home)
Un verrou partagé sérialise l'accès aux ressources globales ; le nommage reste plat, premier arrivé gagne le nom.
- ✅ Détecte les vraies collisions de nom au lieu de les namespacer.
- ➖ Introduit de la **coordination** entre daemons (contention, staleness, nettoyage de locks orphelins) — violation directe de YAGNI.
- ➖ N'isole pas : si A possède `foo`, B ne peut tout simplement **pas** créer `foo`. C'est dégradant, pas isolant.
- ➖ Le home Discord est partagé entre deux **bots** différents : pas de mécanisme de lock natif côté Discord.

### Recommandation
**Approche A.** Elle résout les quatre collisions avec un seul mécanisme (un préfixe
dérivé d'une identité déjà disponible), sans infra Discord ni coordination, et fonctionne
identiquement pour catégorie et forum. B peut venir **plus tard** comme amélioration
d'affichage *par-dessus* A (les deux composent), mais n'est pas requise. C est écartée.

## 6. Affichage

- Titre Discord: `instanceID__name`. Lisible, trivllement parsable, trie naturellement par owner.
  - Variante cosmétique (non bloquante): tag `[instanceID] name`. Plus joli mais le `[` n'est pas dans le slug forum idéal ; à trancher à l'implémentation, n'affecte pas le design.
- `/session list` : affiche le **nom logique** (le state est déjà filtré à l'instance courante), inutile de polluer avec le préfixe. Optionnellement afficher `instanceID` en en-tête de la liste.
- Status embed (statusLoop) : préfixer le titre par `instanceID` pour distinguer les deux daemons s'ils postent dans le même `StatusChannel`.

## 7. Cas limites

- **Collision résiduelle de nom logique entre instances** : deux instances peuvent toujours avoir chacune une session `foo`. C'est **voulu** et sans danger (branche/worktree/titre divergent). Le seul point de contact est l'œil humain dans le forum → résolu par le préfixe de titre.
- **Garde d'appartenance** : un daemon ne peut close/archiver que ce qui est dans **son** state. Comme `ArchiveChannel(sess.ChannelID)` agit sur un channel id stocké localement, il ne peut pas archiver le post d'un autre owner même de nom logique identique. Aucune garde supplémentaire nécessaire ; on **documente** l'invariant et on ajoute un test.
- **Changement d'`instanceID`** après création de sessions : refusé au démarrage (state figé) pour ne pas orpheliner branches/worktrees.
- **Deux instances, même `instanceID`** (mauvaise config, ex. copié-collé) : non détectable de façon fiable sans coordination → **documenté comme contrat utilisateur** : `instanceID` doit être unique par daemon. Mitigation légère possible : inclure `instanceID` dans le nom du fichier state pour rendre l'erreur visible localement.

## 8. Migration des sessions existantes

Sessions créées avant cette spec ont un worktree `<repo>/.dctl-sessions/<name>` et une
branche `session/<name>` non préfixés, et un `State.InstanceID` vide.

Stratégie (rétrocompat, pas de big-bang) :
1. Si `State.InstanceID` est vide et qu'il existe déjà des sessions → on **conserve le mode legacy** (worktreer sans préfixe) tant que ces sessions vivent. `QualifiedName` renvoie le nom nu. Pas de renommage forcé de branches/worktrees existants.
2. À la première résolution d'un `instanceID` non vide sur un state **sans sessions** (ou via une commande `dctl migrate` explicite ultérieure) → on fige l'id ; les **nouvelles** sessions sont namespacées.
3. Commande de migration explicite (hors scope de cette spec, mais prévue) : `git branch -m session/foo session/<id>/foo` + `git worktree move`. Optionnelle, manuelle d'abord.

Principe: **les sessions legacy ne sont jamais cassées** ; le namespacing s'applique aux
sessions créées après adoption d'un `instanceID`.

## 9. Critères de succès

1. Deux daemons (instanceID `alice` / `bob`) partageant le même forum créent chacun une session `foo` **sans erreur** ; on observe `session/alice/foo` + `session/bob/foo`, `.dctl-sessions/alice/foo` + `.dctl-sessions/bob/foo`, et deux posts distincts.
2. `bob` close `foo` → seul le post/worktree/branche de `bob` est touché ; ceux d'`alice` intacts (test d'appartenance).
3. Un daemon sans `instanceID` et avec des sessions legacy continue de fonctionner à l'identique (rétrocompat).
4. Démarrer un daemon avec un `instanceID` différent de celui figé dans le state → échec explicite.
5. `instanceID` validé par regex ; impossible d'injecter `/`, `..`, ou `__` dans l'id.
6. Tests unitaires : `QualifiedName`, `worktree.path`/branche préfixées, garde d'appartenance dans `sessionClose`.
