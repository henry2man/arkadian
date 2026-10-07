# Arca --- Investigation

> Investigación inicial sobre el espacio de herramientas para gestionar,
> almacenar, replicar y mover modelos de IA entre almacenamiento local,
> discos externos, NAS y otros *vaults*.
>
> **Estado:** documento de investigación; no pretende sustituir el
> diseño ni la implementación existente de Arca. Las propuestas están
> pensadas para poder incorporarse incrementalmente.

## 1. Idea central

Arca puede ocupar un espacio algo distinto al de los gestores actuales
de modelos:

**gestionar la biblioteca física de modelos de IA distribuida entre
distintas ubicaciones de almacenamiento.**

El problema no es solamente descargar un modelo desde Hugging Face. En
una máquina como DGX Spark, donde los modelos pueden ocupar decenas o
cientos de GB, aparece rápidamente la necesidad de saber:

-   qué modelos tenemos;
-   qué versión/revisión concreta tenemos;
-   dónde está cada copia;
-   cuánto ocupa;
-   si existe una copia verificada antes de borrar la copia
    rápida/local;
-   cómo mover modelos entre NVMe local, SSD USB, NAS u otra máquina;
-   cómo recuperar un modelo;
-   cómo comprobar que las copias siguen siendo idénticas;
-   cómo liberar almacenamiento caro sin perder el artefacto;
-   cómo importar modelos ya existentes sin volver a descargarlos.

La abstracción propuesta es:

``` text
                       Hugging Face
                            │
                           pull
                            │
                            ▼
                    ┌───────────────┐
                    │  vault:local  │
                    │   DGX Spark   │
                    └───────┬───────┘
                            │
                ┌───────────┴───────────┐
                │                       │
              copy                    copy
                │                       │
                ▼                       ▼
        ┌───────────────┐       ┌───────────────┐
        │   vault:ssd   │       │   vault:nas   │
        │ external NVMe │       │  NAS / rsync  │
        └───────────────┘       └───────────────┘
```

Arca sería, por tanto, menos un *model registry* tradicional y más un:

> **storage manager / artifact manager especializado en modelos de IA.**

------------------------------------------------------------------------

## 2. Proyectos OSS relevantes

Solo se incluyen referencias externas a proyectos open source.

### 2.1 modelshelf

Repositorio: https://github.com/KOIYAL/modelshelf

Probablemente es el proyecto más cercano conceptualmente en cuanto a
**descubrimiento, identidad, deduplicación y gestión de modelos
existentes**.

Características interesantes:

-   Rust + CLI.
-   Sin daemon.
-   Descubre modelos existentes de Hugging Face, Ollama, LM Studio, Jan,
    GPT4All y directorios personalizados.
-   Registro local.
-   Store *content-addressed*.
-   Identificación/detección de duplicados mediante hash.
-   Uso de hardlinks para compartir una única copia física.
-   Descargas reanudables.
-   Journal para operaciones de deduplicación.
-   Convención de almacenamiento documentada independientemente de la
    implementación.

Su estructura es especialmente interesante:

``` text
~/.modelshelf/
├── registry.json
├── registry.lock
├── blobs/
├── downloads/
├── journal/
├── hash_cache.json
├── catalog.json
└── config.json
```

Su objetivo principal, sin embargo, es distinto al de Arca: **evitar que
distintas aplicaciones de una misma máquina mantengan copias redundantes
del mismo modelo**.

Su especificación explícitamente trata el *shelf* como una convención
local y no soporta el filesystem principal sobre almacenamiento de red.

#### Ideas aprovechables para Arca

-   Identidad independiente de la ruta física.
-   Hashes como base de verificación.
-   Importar/adoptar modelos existentes.
-   Cache de hashes para evitar recalcular decenas de GB continuamente.
-   Operaciones con journal.
-   Descargas reanudables.
-   Dry-run para operaciones destructivas.
-   Separar el registro lógico del almacenamiento físico.

------------------------------------------------------------------------

### 2.2 UMR --- Unified Model Registry

Repositorio: https://github.com/EvanZhouDev/umr

UMR intenta mantener una única copia centralizada de cada modelo y
hacerla utilizable desde distintas aplicaciones locales.

Actualmente trabaja principalmente con:

-   Hugging Face;
-   GGUF locales;
-   LM Studio;
-   Ollama;
-   Jan.

Puede utilizar referencias o hardlinks para evitar copias innecesarias.

#### Ideas aprovechables

Arca debería distinguir entre:

``` text
MODEL / ARTIFACT
        │
        ├── physical copy in vault A
        ├── physical copy in vault B
        └── consumer/runtime references
```

Esto permitiría que, en el futuro, Arca conozca no solamente dónde está
un modelo sino también quién lo consume.

Ejemplo:

``` text
Qwen3.8-27B-NVFP4
├── local:/models/qwen38
├── nas:/models/qwen38
└── consumers
    ├── sparkrun:qwen38
    └── vllm:qwen38
```

No es necesario para una primera versión, pero el modelo de datos
debería evitar impedirlo.

------------------------------------------------------------------------

### 2.3 llmman

Repositorio: https://github.com/llmmanorg/llmman

Es probablemente el proyecto que más conviene estudiar desde el punto de
vista de **UX de transferencia**.

Utiliza OCI como formato de almacenamiento y permite operaciones como:

``` bash
llmman pull ...
llmman push ...
llmman transfer ...
llmman resolve ...
llmman list ...
```

Puede transferir desde Hugging Face hacia un registry OCI sin necesidad
de materializar previamente una copia completa en el almacenamiento
local.

También integra ejecución mediante runtimes como llama.cpp, vLLM, SGLang
y MLX.

#### Diferencia respecto a Arca

llmman tiende a convertir OCI en la abstracción universal del modelo.

Arca no necesita hacerlo.

Un vault debería poder ser simplemente:

``` text
/mnt/models
/media/usb/models
ssh://spark2/models
ssh://nas/models
nfs://...
s3://...
```

El formato original del modelo debería conservarse siempre que sea
posible.

#### Idea muy interesante

Separar conceptualmente:

``` text
source -> destination
```

de:

``` text
source -> local -> destination
```

Una transferencia NAS → Spark2 no debería tener que pasar por el
almacenamiento principal del Spark que ejecuta Arca si el backend
permite evitarlo.

Esto puede evolucionar hacia transferencias *vault-to-vault* eficientes.

------------------------------------------------------------------------

### 2.4 DVC

Repositorio: https://github.com/iterative/dvc

DVC resuelve un problema más genérico: versionado y distribución de
datasets/modelos asociados a proyectos.

Su concepto de **remote** es muy relevante:

-   filesystem local;
-   discos montados;
-   NAS;
-   SSH/SFTP;
-   S3;
-   Azure;
-   Google Cloud;
-   WebDAV;
-   etc.

Conceptualmente:

``` text
Git remote  -> código
DVC remote  -> datos grandes
Arca vault  -> modelos
```

#### Diferencia importante

DVC gira alrededor de un proyecto/versionado DVC.

Arca puede mantener una **biblioteca global de modelos**, independiente
de cualquier repositorio de código.

#### Idea aprovechable

La abstracción `vault` debería ocultar el transporte.

Los comandos de alto nivel no deberían saber si detrás existe:

``` text
filesystem
rsync
SSH
S3
NFS
USB
NAS
```

------------------------------------------------------------------------

### 2.5 KitOps

Repositorio: https://github.com/kitops-ml/kitops

KitOps es un proyecto CNCF para empaquetar, versionar y distribuir
artefactos de IA utilizando OCI.

Utiliza SHA-256, artefactos inmutables y permite firma criptográfica.

Puede empaquetar no solamente pesos sino también datasets, prompts,
configuración y otros elementos.

#### Aplicación a Arca

No parece necesario convertir Arca en otro sistema OCI.

Sí merece la pena conservar dos ideas:

1.  **identidad verificable del artefacto**;
2.  posibilidad futura de que un artefacto incluya más que los pesos.

Por ejemplo:

``` text
artifact
├── model files
├── tokenizer
├── config
├── chat template
├── metadata
└── arca manifest
```

------------------------------------------------------------------------

## 3. Hueco de producto

Los proyectos investigados tienden a resolver alguno de estos problemas:

  -----------------------------------------------------------------------
  Proyecto                            Problema principal
  ----------------------------------- -----------------------------------
  modelshelf                          biblioteca local compartida y
                                      deduplicación

  UMR                                 modelo local único compartido entre
                                      aplicaciones

  llmman                              distribución/ejecución mediante OCI

  DVC                                 versionado y remotes para
                                      datos/modelos de proyectos

  KitOps                              empaquetado/versionado OCI de
                                      activos AI/ML

  **Arca**                            **gestión física distribuida de una
                                      biblioteca personal/privada de
                                      modelos**
  -----------------------------------------------------------------------

La hipótesis de producto para Arca sería:

> **Sé qué modelos tengo, qué artefacto exacto es cada uno, dónde
> existen copias verificadas y puedo moverlos de forma segura entre mis
> distintos niveles de almacenamiento.**

Esto encaja especialmente bien con estaciones AI locales:

``` text
fast / expensive storage
        │
        │ evict / restore
        ▼
large / slower storage
        │
        │ replicate
        ▼
backup storage
```

Es, en la práctica, **tiered storage especializado para modelos**.

------------------------------------------------------------------------

## 4. Modelo conceptual

### Model

Identidad humana/lógica:

``` text
Qwen3.8-Flash-Next-NVFP4
```

### Artifact

Una versión material y reproducible concreta:

``` text
model: Qwen3.8-Flash-Next-NVFP4
source: hf://Mia-AiLab/Qwen3.8-Flash-Next-NVFP4
revision: 925d7be...
digest: sha256:...
size: ...
```

Dos revisiones del mismo repositorio deben poder ser artefactos
diferentes.

### Copy

Materialización de un artefacto dentro de un vault:

``` text
artifact abc123
├── local:/models/qwen38
├── ssd:/arca/qwen38
└── nas:/ai/models/qwen38
```

Cada copia puede tener estado:

``` text
unknown
present
verified
incomplete
corrupt
missing
```

### Vault

Backend de almacenamiento identificado por un nombre estable:

``` text
local
ssd
nas
spark2
archive
```

Ejemplo de configuración conceptual:

``` yaml
vaults:
  local:
    type: filesystem
    path: /models

  ssd:
    type: filesystem
    path: /mnt/models

  nas:
    type: rsync
    url: nas:/volume1/ai/models
```

------------------------------------------------------------------------

## 5. UX propuesta

La CLI debería intentar parecer más a una combinación de `git`, `docker`
y herramientas Unix que a un gestor ML complejo.

### Configurar vaults

``` bash
arca vault add local /models
arca vault add ssd /mnt/models
arca vault add nas ssh://nas/volume1/models

arca vault list
```

### Descargar

``` bash
arca pull hf://Qwen/Qwen3.5-35B-A3B
```

Opcionalmente:

``` bash
arca pull hf://Qwen/Qwen3.5-35B-A3B --to local
```

### Inventario

``` bash
arca list
```

Salida deseable:

``` text
MODEL                         SIZE     LOCAL   SSD   NAS
Qwen3.8-27B-NVFP4             19 GB      ●      ●     ●
Qwen3.8-Flash-Next-NVFP4      48 GB      ●      ○     ●
Nemotron-3.5-30B-NVFP4        24 GB      ●      ○     ○
GLM-4.7-GGUF-Q4               76 GB      ○      ●     ●
```

Esta vista puede acabar siendo una de las funcionalidades más valiosas
de Arca.

### Localizar

``` bash
arca where qwen3.8-flash
```

``` text
local   verified   /models/qwen38-flash
nas     verified   /volume1/models/qwen38-flash
ssd     -
```

### Copiar

``` bash
arca cp qwen3.8-flash local nas
```

### Mover

``` bash
arca mv qwen3.8-flash local ssd
```

Semánticamente `mv` debería ser:

``` text
copy
verify destination
delete source
```

Nunca simplemente `rsync --remove-source-files` sin garantías.

### Replicar

``` bash
arca replicate qwen3.8-flash --to ssd,nas
```

### Backup / restore

Alias orientados a intención:

``` bash
arca backup qwen3.8-flash nas
arca restore qwen3.8-flash nas local
```

Internamente pueden ser simplemente operaciones `cp`, pero mejoran mucho
la UX.

### Sincronización

``` bash
arca sync local nas
```

Debe definirse cuidadosamente si significa:

-   sincronización unidireccional;
-   reconciliación de inventario;
-   replicación de artefactos;
-   espejo exacto.

Conviene evitar semánticas destructivas implícitas.

------------------------------------------------------------------------

## 6. Seguridad de datos

Este debería ser uno de los puntos diferenciadores.

### Regla fundamental

**Arca no debería eliminar la última copia conocida de un artefacto
salvo petición explícita y claramente peligrosa.**

Ejemplo:

``` bash
arca rm qwen38 --from local
```

Si no existe otra copia verificada:

``` text
ERROR: local contains the only verified copy of qwen38.

Use --force-last-copy to remove it anyway.
```

### Eviction

Operación especialmente útil:

``` bash
arca evict qwen38
```

Semántica:

> libera la copia del almacenamiento rápido/local solamente si existe al
> menos otra copia verificada en un vault considerado seguro.

Ejemplo:

``` text
qwen38
local   verified
nas     verified

$ arca evict qwen38

Removing local copy...
Freed: 48.2 GB
Remaining verified copies: nas
```

Esto convierte Arca en algo más interesante que un wrapper sobre rsync.

------------------------------------------------------------------------

## 7. Verificación e integridad

### Manifest

Cada artefacto debería tener suficiente metadata para verificarlo.

Conceptualmente:

``` yaml
id: qwen38-flash-next-nvfp4@925d7be
source:
  type: huggingface
  repo: Mia-AiLab/Qwen3.8-Flash-Next-NVFP4
  revision: 925d7be...

files:
  - path: model-00001-of-00008.safetensors
    size: ...
    sha256: ...
  - path: tokenizer.json
    size: ...
    sha256: ...
```

### Verificación

``` bash
arca verify qwen38
arca verify qwen38 --vault nas
arca verify --vault nas
```

Podrían existir niveles:

``` text
quick    metadata + size + timestamps/cache
full     hashes completos
```

Hashing de un modelo de cientos de GB es costoso, por lo que merece la
pena mantener un **hash cache**.

------------------------------------------------------------------------

## 8. Descubrimiento e importación

Arca no debería exigir que todo haya sido descargado por Arca.

Comandos potenciales:

``` bash
arca scan /models
arca scan ~/.cache/huggingface
```

o:

``` bash
arca import /models/Qwen...
```

Resultado:

``` text
Found:
  Qwen3.8-27B-NVFP4
  19.4 GB
  Hugging Face metadata detected
  revision: ...

Register as artifact? yes
```

Más adelante podrían añadirse adaptadores de descubrimiento:

``` text
Hugging Face cache
Ollama
LM Studio
llmman/OCI
arbitrary directories
```

**Adoptar lo que ya existe es mejor que obligar a reorganizarlo todo.**

------------------------------------------------------------------------

## 9. Content-addressed storage: usar con cuidado

modelshelf demuestra que un store CAS funciona muy bien para
deduplicación local.

Arca podría utilizar hashes internamente sin imponer necesariamente una
estructura física opaca.

Por ejemplo, un NAS humano:

``` text
/models/
  Qwen/
    Qwen3.8-Flash-Next-NVFP4/
```

puede ser más útil que:

``` text
/blobs/
  sha256-a31f...
  sha256-f82c...
```

Propuesta:

> **content-addressed identity, human-readable physical layout.**

El CAS físico puede quedar como backend opcional si aporta ventajas
posteriormente.

------------------------------------------------------------------------

## 10. Vault capabilities

No todos los vaults tienen las mismas capacidades.

Ejemplo:

``` text
filesystem:
  read
  write
  rename
  hardlink
  reflink
  direct-copy

rsync:
  read
  write
  resume
  remote-copy

s3:
  read
  write
  multipart
  server-side-copy
```

Arca debería preguntar al backend qué puede hacer y escoger la
estrategia óptima.

Esto evita llenar el core de condiciones como:

``` text
if nas...
if usb...
if ssh...
```

------------------------------------------------------------------------

## 11. Transfer engine

El core debería pensar en:

``` text
transfer(sourceVault, destinationVault, artifact)
```

y no en:

``` text
rsync(...)
```

Posibles estrategias:

``` text
filesystem -> filesystem     cp / reflink / hardlink
filesystem -> ssh            rsync
ssh -> filesystem            rsync
ssh -> ssh                   remote rsync/direct transfer
S3 -> filesystem             multipart download
filesystem -> S3             multipart upload
```

Una propiedad especialmente deseable:

> **Las transferencias deben poder reanudarse.**

Con modelos de 50--500 GB, una transferencia fallida no debería empezar
desde cero.

------------------------------------------------------------------------

## 12. Estado de operaciones

Mantener operaciones explícitas permitiría:

``` bash
arca jobs
```

``` text
ID       ARTIFACT        FROM    TO     PROGRESS
tx-182   qwen38-flash    local   nas    73%
```

Y:

``` bash
arca resume tx-182
```

No es imprescindible para la primera versión si rsync ya proporciona
esta capacidad, pero el modelo interno debería permitir añadirlo.

------------------------------------------------------------------------

## 13. Storage awareness

``` bash
arca space
```

Ejemplo:

``` text
VAULT       USED       FREE       MODELS
local       2.1 TB     1.5 TB        17
ssd         3.7 TB     300 GB        31
nas         11 TB      7 TB          84
```

Esto habilita posteriormente decisiones inteligentes:

``` bash
arca evict --free 500GB
```

o incluso:

``` bash
arca ensure qwen38 --on local
```

que podría restaurarlo automáticamente desde la mejor copia disponible.

------------------------------------------------------------------------

## 14. Políticas futuras

Sin complicar la V1, el modelo puede dejar abierta una política como:

``` yaml
policies:
  important:
    replicas: 2
    vaults:
      - nas
      - archive
```

Entonces:

``` bash
arca status
```

podría avisar:

``` text
Qwen3.8-Flash
policy: important
required replicas: 2
verified replicas: 1
STATUS: under-replicated
```

Esto acerca Arca conceptualmente a un pequeño sistema de almacenamiento
distribuido, pero manteniendo una CLI sencilla.

------------------------------------------------------------------------

## 15. Garbage collection

La eliminación debería separar:

``` text
unregister
delete copy
delete artifact
garbage collect
```

No deberían ser accidentalmente la misma operación.

Posibles comandos:

``` bash
arca rm qwen38 --from local
arca forget qwen38
arca gc local
```

`gc` podría detectar:

-   descargas parciales antiguas;
-   blobs huérfanos;
-   artefactos no registrados;
-   revisiones antiguas;
-   duplicados;
-   temporales de transferencias.

Siempre con:

``` bash
arca gc --dry-run
```

como comportamiento prudente por defecto.

------------------------------------------------------------------------

## 16. Provenance

Para modelos de IA es especialmente útil conservar:

``` text
source
repository
revision
download date
original filenames
hashes
quantization
format
architecture
```

Ejemplo:

``` bash
arca inspect qwen38
```

``` text
Name:         Qwen3.8-Flash-Next-NVFP4
Source:       Hugging Face
Repository:   Mia-AiLab/...
Revision:     925d7be...
Format:       safetensors
Quantization: NVFP4
Size:         48.2 GB

Copies:
  local       verified
  nas         verified
```

Así Arca funciona también como inventario reproducible.

------------------------------------------------------------------------

## 17. Alias

Separar identidad de alias resulta útil:

``` bash
arca alias set daily-driver qwen38-flash@925d7be
```

Luego:

``` bash
arca restore daily-driver nas local
```

Esto permite cambiar el modelo asociado sin perder la identidad
histórica de los artefactos.

------------------------------------------------------------------------

## 18. Diseño incremental recomendado

No es necesario implementar todo esto de una vez.

### Core

Las abstracciones que sí parece valioso acertar pronto son:

``` text
Model
Artifact
Copy
Vault
Transfer
```

Y especialmente:

``` text
Artifact != path
```

Un modelo no debería identificarse por `/models/foo`.

### Primera capa útil

``` text
vault add/list
pull
list
where
cp
verify
rm
```

### Segunda capa

``` text
scan/import
mv
backup/restore
replicate
space
evict
```

### Posteriormente

``` text
policies
gc
jobs/resume
S3
cross-vault direct transfer
runtime consumers
multi-machine inventory
```

------------------------------------------------------------------------

## 19. Lo que evitaría

### No crear otro Hugging Face

Arca no necesita catálogo público, hosting de modelos ni comunidad de
publicación.

### No imponer OCI

OCI puede ser un backend/import/export interesante, pero no parece
necesario convertir todos los modelos a OCI.

### No imponer un formato físico propio

Siempre que sea posible, un modelo debería seguir siendo utilizable
directamente por:

``` text
vLLM
llama.cpp
Transformers
SGLang
MLX
sparkrun
```

### No hacer que Arca sea requisito para usar el modelo

Si Arca desaparece mañana, los modelos deberían seguir siendo
carpetas/ficheros normales.

### No confundir backup con sync

`backup`, `replicate`, `sync`, `move` y `delete` tienen garantías
distintas. Conviene que la CLI lo refleje.

------------------------------------------------------------------------

## 20. Principios de diseño sugeridos

1.  **Local first.**
2.  **No daemon required**, salvo que una función futura lo justifique.
3.  **Filesystem friendly.**
4.  **Modelos utilizables fuera de Arca.**
5.  **Identidad independiente de ubicación.**
6.  **Transferencias reanudables.**
7.  **Verificación antes de borrar.**
8.  **Nunca perder silenciosamente la última copia.**
9.  **Adoptar modelos existentes en lugar de exigir redescargas.**
10. **Vaults como abstracción, transporte como implementación.**
11. **Metadata pequeña; pesos intactos.**
12. **Operaciones destructivas explícitas.**
13. **CLI usable por humanos y automatizable por agentes/scripts.**

------------------------------------------------------------------------

## 21. Posible visión

La idea puede resumirse en:

``` text
                 ARCA
                  │
         artifact inventory
                  │
       ┌──────────┼──────────┐
       │          │          │
      DGX        SSD        NAS
     Spark
       │
       └──── model available here
```

Hoy:

``` bash
arca where qwen38
arca cp qwen38 local nas
arca evict qwen38
```

Mañana:

``` bash
arca ensure qwen38 --on spark1
```

Arca determina que no está en `spark1`, encuentra una copia verificada
en el NAS, comprueba espacio, la restaura y verifica el resultado.

Ese salto ---de **copiar ficheros** a **gestionar disponibilidad de
artefactos**--- parece ser la dirección con mayor potencial.

------------------------------------------------------------------------

## 22. Conclusión

Existe bastante tecnología OSS alrededor de gestión de modelos, pero los
proyectos revisados se concentran en:

-   deduplicación local;
-   compartir modelos entre aplicaciones;
-   OCI/model registries;
-   versionado de ML;
-   ejecución de modelos.

Hay espacio para una herramienta cuyo objeto principal sea:

> **la ubicación, replicación, integridad y ciclo de vida físico de una
> biblioteca de modelos distribuida entre varios niveles de
> almacenamiento.**

Para Arca, las ideas que parecen aportar más valor sobre una
implementación ya existente son:

-   formalizar `Vault`, `Artifact` y `Copy`;
-   identidad mediante revisión + hashes, independiente de paths;
-   matriz `modelo × vault`;
-   `where`;
-   `verify`;
-   `replicate`;
-   `backup` / `restore`;
-   `evict` seguro;
-   importación/adopción de modelos existentes;
-   transferencias reanudables;
-   cache de hashes;
-   protección de la última copia;
-   separación entre identidad content-addressed y layout físico
    legible;
-   backends de vault basados en capacidades;
-   dejar abierta la puerta a políticas de replicación y almacenamiento
    por niveles.

Estas funcionalidades pueden añadirse de forma incremental sin convertir
Arca en una plataforma ML ni obligar a rehacer el trabajo existente.

------------------------------------------------------------------------

## Referencias OSS

-   modelshelf --- https://github.com/KOIYAL/modelshelf
-   modelshelf on-disk specification ---
    https://github.com/KOIYAL/modelshelf/blob/main/docs/SPEC.md
-   UMR --- https://github.com/EvanZhouDev/umr
-   llmman --- https://github.com/llmmanorg/llmman
-   DVC --- https://github.com/iterative/dvc
-   KitOps --- https://github.com/kitops-ml/kitops
