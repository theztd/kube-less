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

### 1.3 Hydration Engine – Deployment → CRI konfigurační objekty

Nový balík `internal/hydration` s funkcí:

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

### 1.4 Engine – reconciliační smyčka

`StartReconciliationLoop` musí být plně funkční:

```
každých <sync_interval>:
  pro každý workload ve Store:
    desired  = workload.Manifest (parsovaný Deployment)
    actual   = CRI stav (ListPodSandbox + ListContainers)
    diff     = compare(desired, actual)
    aplikuj diff (create / update / delete)
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

## 5. Přehled nových/změněných souborů

```
internal/
  hydration/
    builder.go          # BuildPodSandboxConfig, BuildContainerConfigs
    builder_test.go
  probe/
    runner.go           # ProbeRunner
    runner_test.go
  runtime/
    client.go           # +RunPodSandbox, +StopPodSandbox, +RemovePodSandbox,
                        #  +CreateContainer, +StartContainer, +StopContainer,
                        #  +RemoveContainer, +ListContainers, +PullImage
  engine/
    store.go            # +configMaps, +secrets, +fileToWorkloads,
                        #  +ConfigHash, +Ready, +SandboxIP, +StartedAt,
                        #  +ContainerIDs
    engine.go           # plná reconciliační smyčka, handleRemove,
                        #  startup reconciliation
    reconciler.go       # (nový) compare() + applyDiff()
  api/
    server.go           # +GET /endpoints
  config/
    config.go           # +DataDir string
configs/
  config.yaml           # +data_dir: /var/lib/kube-less
```

---

## 6. Pořadí implementace (milníky)

### Milestone A – Základní create/delete (unblockuje vše ostatní)
1. `runtime/client.go` – přidat všechny chybějící CRI metody
2. `hydration/builder.go` – základní BuildPodSandboxConfig + BuildContainerConfigs
   (bez ConfigMap injekcí, jen literální env a image)
3. `engine/engine.go` – handleUpdate volá create pipeline (PullImage →
   RunPodSandbox → CreateContainer → StartContainer)
4. `engine/engine.go` – handleRemove volá delete pipeline

### Milestone B – Reconciliace a odolnost vůči restartu
5. `engine/store.go` – fileToWorkloads, ConfigHash, ContainerIDs, SandboxIP,
   StartedAt
6. `engine/reconciler.go` – compare() + applyDiff()
7. `engine/engine.go` – plná StartReconciliationLoop
8. Startup reconciliation (načtení souborů → sync s CRI před spuštěním Watcheru)

### Milestone C – ConfigMap a Secret injekce
9. `engine/store.go` – configMaps, secrets maps
10. `engine/engine.go` – routování ConfigMap/Secret při parsování
11. `hydration/builder.go` – env reference (configMapKeyRef, secretKeyRef)
12. `hydration/builder.go` – FS mount (zápis na hostPath)
13. Cleanup hostPath souborů při smazání CM / Deploymetu

### Milestone D – HTTP sondy a endpoints API
14. `engine/store.go` – Ready bool, ReadyContainers int
15. `probe/runner.go` – ProbeRunner s HTTP GET sondou
16. Integrace ProbeRunneru do main.go
17. `api/server.go` – GET /endpoints

### Milestone E – Finalizace
18. `config/config.go` – DataDir
19. Testy pro hydration a probe balíky
20. Aktualizace README a examples/manifests (přidat readinessProbe ukázku)

---

## 7. Otevřené otázky a rozhodnutí

| Otázka | Rozhodnutí pro 0.1.0 |
|---|---|
| Jak zjistit IP sandboxu? | `PodSandboxStatus` CRI call po `RunPodSandbox`, pole `network.ip` |
| Jak ošetřit chybějící image? | PullImage vždy před spuštěním (dle pull-policy anotace) |
| Replicas > 1 | Vytvořit N sandboxů se suffixem `-0`, `-1`, … v metadatech |
| Update strategie | Stop+Remove starého, Start nového (žádný rolling update) |
| Secrets v paměti | Ukládat jen na dobu hydration, neperzistovat na disk v plaintextu |
| Liveness probe | Není v scope 0.1.0, přidáme v 0.2.0 |
| Port v endpoint bez readiness probe | Přidat ihned po StartContainer s optimistickým Ready=true |
