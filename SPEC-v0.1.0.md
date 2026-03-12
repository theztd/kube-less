# kube-less v0.1.0 – Specifikace implementace

Cíl: agent schopný řídit životní cyklus kontejnerů na jednom uzlu pouze pomocí
nahrávání/editace YAML souborů, odolný vůči pádu a restartu, s podporou
ConfigMap injekcí a HTTP readiness sondami.

---

## 1. Pod lifecycle (create / update / delete)

### 1.1 Stav „Store" a jeho překlad do CRI operací

Store dnes uchovává `WorkloadState` jen v paměti a žádné CRI operace nevolá.
Potřebujeme přidat:

- mapování souboru na seznam workload klíčů (`fileToWorkloads map[string][]string`)
  uložené v Store; nutné pro zpracování smazání souboru
- pole `ContainerIDs []string` do `WorkloadState` (CRI vrací sandbox ID i
  container ID zvlášť)
- pole `DesiredReplicas int` a `ActualReplicas int` pro budoucí škálování
  (v 0.1.0 stačí replicas = 1 nebo N identických sandboxů)

### 1.2 CRI client – chybějící metody

Přidat do `internal/runtime/client.go`:

```
RunPodSandbox(ctx, config *runtimeapi.PodSandboxConfig) (sandboxID string, err error)
StopPodSandbox(ctx, sandboxID string) error
RemovePodSandbox(ctx, sandboxID string) error
ListContainers(ctx, filter *runtimeapi.ContainerFilter) ([]*runtimeapi.Container, error)
CreateContainer(ctx, sandboxID string, config *runtimeapi.ContainerConfig,
                sbConfig *runtimeapi.PodSandboxConfig) (containerID string, err error)
StartContainer(ctx, containerID string) error
StopContainer(ctx, containerID string, timeout int64) error
RemoveContainer(ctx, containerID string) error
PullImage(ctx, image string) error
```

Každá metoda nastavuje vlastní timeout přes `context.WithTimeout` (doporučeno 30 s
pro PullImage, 10 s pro ostatní).

### 1.3 Překlad Deployment → CRI konfigurační objekty

Součást balíku `internal/cri`:

```go
func BuildPodSandboxConfig(dep *appsv1.Deployment) *runtimeapi.PodSandboxConfig
func BuildContainerConfigs(dep *appsv1.Deployment, sbConfig *runtimeapi.PodSandboxConfig,
    cms map[string]*v1.ConfigMap, secrets map[string]*v1.Secret,
) ([]*runtimeapi.ContainerConfig, error)
```

Mapování polí Deployment → CRI:

| Deployment pole | CRI pole |
|---|---|
| `metadata.name` + `metadata.namespace` | `PodSandboxConfig.Metadata` |
| `metadata.labels` | `PodSandboxConfig.Labels` |
| `spec.template.spec.containers[].image` | `ContainerConfig.Image.Image` |
| `spec.template.spec.containers[].command` | `ContainerConfig.Command` |
| `spec.template.spec.containers[].args` | `ContainerConfig.Args` |
| `spec.template.spec.containers[].env` (literál) | `ContainerConfig.Envs` |
| `spec.template.spec.containers[].env[].valueFrom.configMapKeyRef` | viz sekce 3 |
| `spec.template.spec.containers[].ports[].containerPort` | `ContainerConfig.PortMappings` |
| `spec.template.spec.containers[].resources.limits` | `ContainerConfig.Linux.Resources` |

Anotace `kube-less.io/pull-policy: always|never|ifnotpresent` na Deployment
přeloží na chování PullImage před spuštěním (default: `ifnotpresent`).

### 1.4 Scheduler – reconciliační smyčka

`StartReconciliationLoop` musí být plně funkční:

```
každých <sync_interval>:
  pro každý workload ve Store:
    desired  = workload.Manifest (parsovaný Deployment)
    actual   = dotaz na CRI runtime (ListPodSandbox + ListContainers)
    diff     = compare(desired, actual)
    aplikuj diff (create / update / delete)
              → pořadí: cri sandbox → cri containers
              (networking zajišťuje containerd+CNI automaticky při RunPodSandbox)
```

`compare` vrátí jednu z akcí:
- `ActionCreate` – sandbox neexistuje, vytvořit
- `ActionRecreate` – sandbox existuje ale má jiný image/config hash
- `ActionDelete` – workload byl odstraněn ze Store
- `ActionNone` – vše sedí

Při `ActionRecreate` nejprve stop+remove starého sandboxu, pak vytvořit nový
(rolling update v 0.1.0 není potřeba – prostý restart stačí).

### 1.5 Mazání – handleRemove

Engine musí před uložením workloadu do Store zapamatovat mapování
`filePath → []workloadKey`. Při smazání souboru:

1. Lookup `fileToWorkloads[filePath]`
2. Pro každý klíč: `engine.deleteWorkload(namespace, name)`
3. `deleteWorkload` zavolá `StopPodSandbox` + `RemovePodSandbox` + `RemoveContainer`
   pro všechny kontejnery daného sandboxu
4. `store.DeleteWorkload(namespace, name)`
5. Smazat záznam z `fileToWorkloads`

---

## 2. Odolnost vůči pádu a restartu

### 2.1 Problém

Po restartu agenta je Store prázdný, ale CRI může mít běžící sandboxes.
Opačný případ: soubory existují, CRI nic neběží.

### 2.2 Startup reconciliation

`SyncStateFromCRI` je základní kostra – potřebuje rozšíření:

1. Načíst všechny manifesty ze všech `manifest_dirs` (synchronně, bez fsnotify)
   a naplnit Store požadovaným stavem.
2. Zavolat `ListPodSandbox` a `ListContainers` – získat skutečný stav.
3. Provést porovnání:
   - Sandbox běží, manifest existuje → aktualizovat `PodSandboxID` a `ContainerIDs` ve Store
   - Sandbox běží, manifest chybí → `StopPodSandbox` + `RemovePodSandbox` (osiřelý sandbox)
   - Manifest existuje, sandbox neběží → zaplánovat vytvoření (přidat do fronty)
4. Teprve poté spustit fsnotify Watcher.

Tímto je zajištěno, že po restartu (i po pádu) je stav konzistentní dřív, než
začnou přicházet events ze souborového systému.

### 2.3 Identifikace „našich" sandboxů

CRI neví nic o kube-less. Sandboxes musíme označit labelem:

```
"kube-less/managed": "true"
"kube-less/namespace": "<namespace>"
"kube-less/name": "<deployment-name>"
```

Tyto labely přidá `BuildPodSandboxConfig`. Při `ListPodSandbox` filtrujeme jen
záznamy s `kube-less/managed=true` – ostatní (ruční, system) ignorujeme.

### 2.4 Config hash pro detekci změny

Do `WorkloadState` přidat `ConfigHash string` – SHA256 ze serializovaného
`PodTemplateSpec`. Při modifikaci souboru se hash přepočítá; pokud je jiný,
reconciliátor provede `ActionRecreate`.

---

## 3. ConfigMap injekce

### 3.1 Store – ukládání ConfigMap a Secret

Store dnes ConfigMap ignoruje. Přidat:

```go
type Store struct {
    workloads  map[string]*WorkloadState
    configMaps map[string]*v1.ConfigMap   // klíč: "namespace/name"
    secrets    map[string]*v1.Secret      // klíč: "namespace/name"
    mu         sync.RWMutex
}
```

Parser vrátí `[]runtime.Object`; Engine rozdělí objekty:
- `*appsv1.Deployment` → `store.UpdateWorkload`
- `*v1.ConfigMap` → `store.UpdateConfigMap`
- `*v1.Secret` → `store.UpdateSecret`

Změna ConfigMap/Secret spustí re-reconciliation všech Deploymentů, které na
daný ConfigMap/Secret odkazují (jednoduše: přepočítat hash a zaplánovat
ActionRecreate pokud je odlišný).

### 3.2 Režim FS mount (volumes)

Deployment:
```yaml
volumes:
  - name: app-config
    configMap:
      name: my-config
volumeMounts:
  - name: app-config
    mountPath: /etc/config
```

`BuildContainerConfigs` přeloží na:

1. Vytvoření dočasného adresáře na hostiteli:
   `/var/lib/kube-less/configmaps/<namespace>/<cm-name>/`
2. Pro každý klíč v `ConfigMap.Data` zapsat soubor:
   `/var/lib/kube-less/configmaps/<namespace>/<cm-name>/<key>`
3. Přidat `ContainerConfig.Mounts`:
   ```
   HostPath: /var/lib/kube-less/configmaps/<namespace>/<cm-name>
   ContainerPath: <mountPath>
   Readonly: true
   ```

Při aktualizaci ConfigMap (přeparsování souboru): přepsat soubory na hostiteli.
Kontejner uvidí změněné soubory bez restartu (inotify uvnitř kontejneru).

Při smazání ConfigMap nebo Deploymetu: smazat adresář na hostiteli.

Kořenový adresář `/var/lib/kube-less/` nastavitelný v `config.yaml` jako
`data_dir` (default: `/var/lib/kube-less`).

### 3.3 Režim env reference

Deployment:
```yaml
env:
  - name: DB_PASSWORD
    valueFrom:
      configMapKeyRef:
        name: my-config
        key: db_password
  - name: API_KEY
    valueFrom:
      secretKeyRef:
        name: my-secret
        key: api_key
```

`BuildContainerConfigs` při sestavování `ContainerConfig.Envs`:
1. Pro literální `env.value` → přidat přímo.
2. Pro `env.valueFrom.configMapKeyRef`:
   - Lookup `store.GetConfigMap(namespace, refName)`
   - Pokud nalezen: přidat `{Key: envName, Value: cm.Data[refKey]}`
   - Pokud nenalezen: vrátit chybu (deployment nejde spustit, zalogovat, přeskočit)
3. Pro `env.valueFrom.secretKeyRef`: stejný postup, ale `secret.Data[refKey]`
   (hodnota je `[]byte`, base64 dekódovat na string).

---

## 4. HTTP readiness sondy a service discovery endpoint

### 4.1 Sonda – definice v manifestu

```yaml
containers:
  - name: web
    readinessProbe:
      httpGet:
        path: /healthz
        port: 8080
      initialDelaySeconds: 5
      periodSeconds: 10
      failureThreshold: 3
      successThreshold: 1
```

### 4.2 Probe runner

Nový balík `internal/probe` s typem `ProbeRunner`:

```go
type ProbeRunner struct {
    store  *engine.Store
    client *http.Client  // s timeoutem 5 s
}

func (r *ProbeRunner) Start(ctx context.Context)
func (r *ProbeRunner) runProbesForWorkload(ws *engine.WorkloadState)
```

- Goroutine per workload, tick dle `periodSeconds`.
- `initialDelaySeconds` se odečte od doby spuštění sandboxu (uložit
  `StartedAt time.Time` do `WorkloadState`).
- HTTP GET na `<sandbox-ip>:<port><path>` (IP sandboxu získat přes
  `PodSandboxStatus` CRI volání – pole `network.ip`).
- Výsledek zapsat do `WorkloadState.ReadyContainers` (count) a
  `WorkloadState.Ready bool`.
- `failureThreshold` a `successThreshold` – udržovat čítač konsekutivních
  výsledků; přepnout `Ready` jen při dosažení prahu.

### 4.3 Service discovery endpoint

Rozšířit `internal/api/server.go`:

Nový endpoint: `GET /endpoints`

Response:
```json
{
  "nginx.default": ["10.0.0.5:80", "10.0.0.6:80"],
  "api.production": ["10.0.0.10:8080"]
}
```

Logika:
1. Projít všechny `WorkloadState` kde `Ready == true`.
2. Z `Manifest.Spec.Template.Spec.Containers[].Ports` vzít `ContainerPort`.
3. IP získat z `WorkloadState.SandboxIP` (nové pole; plněno po `RunPodSandbox`
   voláním `PodSandboxStatus`).
4. Klíč: `<deployment-name>.<namespace>`.
5. Pokud žádný kontejner nemá definovaný port: vypsat endpoint bez portu
   (jen IP) nebo přeskočit – preferuji přeskočit a zalogovat warning.

Endpoint `GET /status` ponechat beze změny (debug účely).

### 4.4 Workload bez readiness sondy

Pokud Deployment nemá definovanou `readinessProbe`:
- Kontejner je považován za `Ready` ihned po `StartContainer` (optimisticky).
- Toto chování lze přepsat anotací `kube-less.io/require-probe: "true"`, pak
  workload zůstane `Ready=false` dokud sondu nedefinuje.

---

## 5. Networking

### 5.0 Zodpovědnosti

Networking je plně delegován na **containerd + CNI**. kube-less runtime
networking sám nezřizuje ani nekonfiguruje.

| Zodpovědnost | Kdo |
|---|---|
| Instalace CNI binárky (`bridge`, `host-local`, `portmap`) | operátor / install skript |
| Zápis CNI conflistu do `/etc/cni/net.d/` | operátor / install skript |
| Vytvoření netns, bridge, přidělení IP | containerd (volá CNI při `RunPodSandbox`) |
| Validace přítomnosti CNI při startu | kube-less (`network/cni.go`) |
| Konfigurace subnetů pro cross-node routing | operátor (statické routy / BGP) |

### 5.0.1 Konfigurace sítě v `config.yaml`

```yaml
network:
  node_subnet: "10.88.0.0/16"       # subnet pro tento node; každý node musí mít unikátní subnet
                                     # pro cross-node routing (např. node1: 10.88.0.0/24, node2: 10.88.1.0/24)
  bridge_name: "kube-less0"          # název bridge interface (default: kube-less0)
  cni_conf_dir: "/etc/cni/net.d"    # adresář s CNI konfiguračními soubory
  cni_bin_dir:  "/opt/cni/bin"      # adresář s CNI binárkami
```

Hodnoty z `network` sekce slouží jako reference pro validaci a pro install skript –
kube-less runtime je do CNI conflistu **nezapisuje**.

### 5.0.2 Startup validace (`network/cni.go`)

Při startu kube-less:
1. Ověřit existenci `cni_conf_dir` a přítomnost alespoň jednoho `*.conflist` nebo `*.conf` souboru.
2. Ověřit přítomnost binárky `bridge`, `host-local`, `portmap` v `cni_bin_dir`.
3. Při chybě: vypsat srozumitelnou chybovou hlášku a ukončit proces s exit kódem 1.

### 5.0.3 Cross-node networking

Každý node má konfigurovaný jiný `node_subnet`. Routing mezi nody je
zajišťován na úrovni infrastruktury (statické routy, BGP) – mimo scope kube-less.
kube-less zajišťuje pouze to, že subnet je v CNI konflistu správně nastaven
(přes install skript čtoucí `config.yaml`).

---

## 6. Architektura balíků


### 5.1 Datový tok

```
fsnotify
   │ (channel)
   ▼
watcher         – příjem raw FS eventů, deduplikace
   │ (channel)
   ▼
parser          – čtení souboru, YAML → runtime.Object, validace
   │             hlásí chyby zpět (log + drop), předává OK objekty
   ▼
scheduler       – porovnání desired vs actual, výpočet diffu,
   │             orchestrace volání CRI + network ve správném pořadí
   ├──► cri     – čistý executor CRI operací (sandbox, container, image)
   └──► network – bridge, iptables, network namespace
```

### 5.2 Správa stavu – pull model

Scheduler si **aktivně dotazuje** CRI runtime pro actual stav
(`ListPodSandbox`, `ListContainers`) – CRI scheduler nezpětně neinformuje.

```
desiredState  ← objekty z parseru (uloženo v interním Store)
actualState   ← dotaz na CRI runtime (před každou reconciliací)
diff          = reconcile(desired, actual)
              → volá cri + network
```

Výhoda: scheduler může rekonstruovat stav z reality i po restartu,
bez přidané vazby v opačném směru.

### 5.3 Pořadí operací (create)

```
1. cri.RunPodSandbox  (containerd automaticky volá CNI plugin → vytvoří netns + bridge + IP)
2. cri.CreateContainer + cri.StartContainer
```

Teardown probíhá v obráceném pořadí. Networking (netns, bridge, iptables) je
plně v zodpovědnosti containerd+CNI – kube-less ho neřídí.

**Předpoklad:** CNI konfigurace (`/etc/cni/net.d/`) a CNI binárky (`/opt/cni/bin/`)
jsou přítomny před spuštěním kube-less. Jejich instalace je zodpovědností
operátora/instalačního procesu, nikoli kube-less runtime.

kube-less při startu ověří přítomnost CNI konfigurace a binárky `bridge`,
`host-local`, `portmap`. Při chybě vypíše chybu a odmítne nastartovat.

### 5.4 Přehled nových/změněných souborů

```
internal/
  watcher/
    watcher.go          # fsnotify wrapper, deduplikace eventů
  parser/
    parser.go           # YAML → runtime.Object, validace
                        # překládá Deployment → interní typy pro scheduler
    parser_test.go
  scheduler/
    scheduler.go        # reconcile loop, orchestrace, startup sync
    store.go            # desired state store (workloads, configMaps, secrets)
                        # +fileToWorkloads, +ConfigHash, +Ready, +SandboxIP,
                        # +StartedAt, +ContainerIDs
    reconciler.go       # compare(desired, actual) → Action
                        # applyDiff() → volá cri + network ve správném pořadí
  cri/
    client.go           # čistý CRI gRPC klient
                        # +RunPodSandbox, +StopPodSandbox, +RemovePodSandbox,
                        # +CreateContainer, +StartContainer, +StopContainer,
                        # +RemoveContainer, +ListContainers, +PullImage
                        # +BuildPodSandboxConfig, +BuildContainerConfigs
                        #  (překlad Deployment → CRI config objekty)
  network/
    cni.go              # validace CNI konfigurace a binárky při startu
  probe/
    runner.go           # ProbeRunner
    runner_test.go
  api/
    server.go           # +GET /endpoints
  config/
    config.go           # +DataDir string, +Network NetworkConfig
configs/
  config.yaml           # +data_dir: /var/lib/kube-less
                        # +network.node_subnet, +network.bridge_name,
                        # +network.cni_conf_dir, +network.cni_bin_dir
```

> **Poznámka k přejmenování:** původní `internal/engine` → `internal/scheduler`,
> původní `internal/hydration` → překlad Deployment→CRI config je součástí
> balíku `cri` (BuildPodSandboxConfig / BuildContainerConfigs),
> původní `internal/manifest` → `internal/watcher` + `internal/parser`.

---

## 7. Pořadí implementace (milníků)

### ✅ Milestone A – Základní create/delete
1. ✅ `cri/client.go` – přidány všechny chybějící CRI metody
   (`RunPodSandbox`, `StopPodSandbox`, `RemovePodSandbox`, `PodSandboxStatus`,
   `ListContainers`, `CreateContainer`, `StartContainer`, `StopContainer`,
   `RemoveContainer`, `PullImage`, `ImageStatus`)
2. ✅ `cri/builder.go` – `BuildPodSandboxConfig` + `BuildContainerConfigs`
   (literální env, `configMapKeyRef`, `secretKeyRef`, port mappings na sandbox úrovni)
   + `GetPullPolicy` (`kube-less.io/pull-policy` anotace)
3. ✅ `network/cni.go` – `ValidateCNI`: ověří conflist/conf soubory + binárky
   (`bridge`, `host-local`, `portmap`) při startu; chyba → exit 1
4. ✅ `scheduler/scheduler.go` – `handleUpdate`: plný create pipeline
   (PullImage dle pull-policy → RunPodSandbox → CreateContainer → StartContainer)
5. ✅ `scheduler/scheduler.go` – `handleRemove`: teardown přes `fileToWorkloads`
   (StopContainer + RemoveContainer → StopPodSandbox + RemovePodSandbox)
6. ✅ `scheduler/reconciler.go` – `Action` typ + `compare()` (None/Create/Recreate/Delete)
7. ✅ `scheduler/store.go` – rozšíření `WorkloadState` o `ContainerIDs`, `SandboxIP`,
   `ConfigHash`, `StartedAt`; Store o `fileToWorkloads`
8. ✅ Testy: `cri/builder_test.go` (11), `network/cni_test.go` (6),
   `scheduler/reconciler_test.go` (5), `scheduler/store_test.go` (+9),
   `config/config_test.go` (11), `api/server_test.go` (5)

### ✅ Milestone B – Reconciliace a odolnost vůči restartu
9. ✅ `scheduler/store.go` – `UpdateWorkload` automaticky počítá `ConfigHash`
   (desired hash); nová `UpdateRuntimeStatus` (nezapisuje ConfigHash)
10. ✅ `scheduler/scheduler.go` – `LoadManifests`: synchronní načtení všech YAML
    před startem watcheru; `loadManifestFile` (desired state only, bez CRI)
11. ✅ `scheduler/scheduler.go` – `SyncStateFromCRI`: fetchuje `ContainerIDs`
    (`ListContainers`), `SandboxIP` (`PodSandboxStatus`), odstraní osiřelé sandboxy;
    zachová desired `ConfigHash`
12. ✅ `scheduler/scheduler.go` – `reconcileAll`: periodický diff (desired vs. actual
    CRI), nil-guard pro `ws.Manifest`; `StartReconciliationLoop` plně funkční
13. ✅ `createWorkload`: stampuje `kube-less/config-hash` jako sandbox anotaci
14. ✅ `main.go` – správná startup sekvence: CNI validace → `LoadManifests` →
    `SyncStateFromCRI` → `StartReconciliationLoop` → watcher
15. ✅ Testy: `scheduler/scheduler_test.go` (14 testů – mock CRI)

### Milestone C – ConfigMap a Secret injekce ✅
16. ✅ `scheduler/store.go` – přidány `configMaps`, `secrets` maps; `fileToCMs`, `fileToSecrets`;
    `recomputeWorkloadHashes()` při každé změně CM/Secret
17. ✅ `scheduler/scheduler.go` – routování `ConfigMap`/`Secret` v `loadManifestFile` a
    `handleUpdate`; cleanup při `handleRemove`
18. ✅ `cri/builder.go` – `BuildContainerConfigs(dep, sbConfig, cms, secrets, dataDir)`:
    FS mount – zápis CM souborů do `<dataDir>/configmaps/<ns>/<cm-name>/`, read-only mount
19. ✅ `scheduler/scheduler.go` – `cleanupConfigMapFiles`: odstraní hostPath adresáře při
    smazání Deploymetu nebo CM souboru
20. ✅ Re-reconciliation: `computeEffectiveHash` zahrnuje data všech referencovaných CM/Secret;
    `computeEffectiveHashWithStore` helper v scheduleru; sandbox anotace `kube-less/config-hash`
    se aktualizuje při create; `reconcileAll` detekuje rozdíl a provede recreate
21. ✅ Testy: `store_test.go` (+10 testů: CM/Secret CRUD, hash recompute), `builder_test.go`
    (+3 testy: volume mount, missing CM error, optional CM skip), `scheduler_test.go`
    (+5 testů: LoadManifests CM/Secret, hash change detection)

### Milestone D – HTTP sondy a endpoints API ✅
22. ✅ `scheduler/store.go` – přidány `Ready bool`, `ReadyContainers int` do `WorkloadState`;
    metoda `SetWorkloadReady(namespace, name string, ready bool)`
23. ✅ `probe/runner.go` – `Runner` s HTTP GET sondou; spravuje goroutiny per-workload;
    respektuje `initialDelaySeconds`, `periodSeconds`, `successThreshold`, `failureThreshold`;
    workloady bez readinessProbe označeny optimisticky Ready=true
24. ✅ Integrace do `scheduler.go` – `SetProbeRunner()`; `createWorkload` volá `Watch`,
    `deleteWorkload` volá `Stop`; `SyncStateFromCRI` obnoví Watch pro already-running workloady
25. ✅ `api/server.go` – `GET /endpoints` vrací `[]Endpoint{namespace, name, ip, ports}`
    pouze pro workloady kde `Ready=true && SandboxIP != ""`
26. ✅ Propojení v `main.go` – `probe.NewRunner(store)` + `sched.SetProbeRunner(probeRunner)`
27. ✅ Testy: `probe/runner_test.go` (7 testů: no-probe ready, HTTP success, HTTP failure 500,
    Stop, resolvePort), `api/server_test.go` (+4 testy: /endpoints empty, ready workload,
    filter logic, 405), `scheduler/store_test.go` (+2 testy: SetWorkloadReady, noop for missing)

### Milestone E – Finalizace ✅
28. ✅ `examples/manifests/nginx-example.yaml` – kompletní příklad: ConfigMap, Secret,
    volume mount, env refs, `readinessProbe.httpGet` s named portem
29. ✅ Doc komentáře doplněny na všechny exportované metody (file→workload/CM/Secret mappings,
    `Action.String()`)
30. ✅ `README.md` – doplněna sekce Readiness Probes, `/endpoints` API, aktualizována
    architektura (Probe Runner), odkaz na ukázkový manifest

---

## 8. Otevřené otázky a rozhodnutí

| Otázka | Rozhodnutí pro 0.1.0 |
|---|---|
| Jak zjistit IP sandboxu? | `PodSandboxStatus` CRI call po `RunPodSandbox`, pole `network.ip` |
| Jak ošetřit chybějící image? | PullImage vždy před spuštěním (dle pull-policy anotace) |
| Replicas > 1 | Vytvořit N sandboxů se suffixem `-0`, `-1`, … v metadatech |
| Update strategie | Stop+Remove starého, Start nového (žádný rolling update) |
| Secrets v paměti | Ukládat jen na dobu hydration, neperzistovat na disk v plaintextu |
| Liveness probe | Není v scope 0.1.0, přidáme v 0.2.0 |
| Port v endpoint bez readiness probe | Přidat ihned po StartContainer s optimistickým Ready=true |
| `kube-less check` subcommand | Není v scope 0.1.0; v budoucnu: ověří přítomnost CNI binárky + CNI konfig, provede dry-run parsování všech manifestů v `manifest_dirs` a skončí s exit kódem 0/1 |
