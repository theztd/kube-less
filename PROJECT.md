Project: EdgePod Runner (EPR)
1. Vision
Vytvořit minimalistický, autonomní agent v jazyce Go, který spravuje životní cyklus kontejnerů na edge uzlu (single node) pomocí standardních Kubernetes manifestů (Deployment, ConfigMap, Secret), ale zcela bez závislosti na Kubernetes Control Plane (etcd, API server, Scheduler).

2. Core Principles
Single Binary: Jediný spustitelný soubor v Go.

CRI-Compatible: Komunikace přímo s containerd nebo CRI-O přes gRPC socket.

Offline-First: Zdrojem pravdy je lokální adresář s YAML soubory.

No Magic: Žádné skryté stavy; co je v souboru, to běží na node.

3. Technical Architecture (Level 1)
A. Input Watcher
Monitoruje adresář /etc/epr/manifests/.

Detekuje změny (přidání, smazání, update) pomocí fsnotify.

Podporované typy: apps/v1.Deployment, v1.ConfigMap, v1.Secret.

B. Hydration Engine (The "MitM" Logic)
Protože chybí API server, EPR musí provést tzv. "Hydrataci":

Deployment -> Pod: Transformuje Deployment na specifikaci Podu (použije template).

Config/Secret Injection: * Pokud Pod odkazuje na ConfigMap/Secret, EPR je vyhledá v /etc/epr/configs/.

Obsah namapuje do kontejneru pomocí hostPath (vytvoří dočasný soubor na hostiteli) nebo jako environment proměnné.

C. Container Lifecycle Manager (CRI)
Implementuje gRPC klienta pro k8s.io/cri-api.

Sync Loop: Porovnává běžící kontejnery v runtime s požadovaným stavem v manifestech.

Garbage Collection: Odstraňuje kontejnery, jejichž manifesty byly smazány.

4. Future Goals (Level 2)
Service Discovery: Po úspěšném startu kontejneru zaregistruje službu do lokálního Consul agenta.

Health Checks: Lokální provádění Liveness/Readiness sond.

5. Development Roadmap (MVP)
Phase 1: Připojení k CRI socketu a výpis běžících podů.

Phase 2: Parsování lokálního YAML souboru (Deployment) a spuštění jednoduchého Nginx podu.

Phase 3: Implementace ConfigMap injekce přes lokální souborový systém.
