# Evidencias del estudio del supervisor

Fecha: 2026-10-04. Alcance: investigación y pruebas pequeñas de viabilidad, no implementación ni certificación de un supervisor.

## Estado local

| Elemento | Evidencia |
| --- | --- |
| Mailer inspeccionado | Git HEAD `96efe7bb1dd1f18a25e2420e723d5ed7a75a137b`, inicialmente limpio |
| Binario de mailer usado | `/nix/store/0b23wbxqgfr4vi93jipzyflz3kd0ffzb-herdr-mailer-0.1.0/bin/herdr-mailer` |
| Herdr cliente y servidor | `herdr status`: 0.9.1, protocolo 22, endpoint compatible, sin reinicio pendiente |
| Esquema nativo | `herdr api schema --output ...`; extracto en [native-methods.json](native-methods.json), hash completo en [client-probes.json](client-probes.json) |
| Worktrees existentes | `herdr worktree list --cwd .` en el checkout del mailer mostró main y `feat/native-runtimes`, ambos abiertos en Herdr |
| Lanes inspeccionado | Git HEAD `d0783e566a4550c9084e33eb7b56333537146967`, limpio; README y `docs/architecture.md` |

Las consultas reales a Herdr fueron de lectura. No se arrancó ni reinició ningún servidor compartido, no se instaló ningún plugin, no se crearon agentes ni se enviaron mensajes a sesiones de modelos. Los daemons temporales de mailer terminaron al cerrar sus pruebas. El worktree que contiene estos documentos es un worktree de documentación del estudio, no un trabajador del futuro supervisor.

## Experimentos ejecutados

Resultados estructurados: [protocol-probes.json](protocol-probes.json) y [client-probes.json](client-probes.json).

| Experimento | Resultado observado | Qué demuestra y qué no |
| --- | --- | --- |
| Herdr `ping` directo | Protocolo 22 | Compatibilidad del endpoint consultado |
| Segunda lectura después de respuesta normal | EOF | Una conexión ordinaria sirve una petición; no reutilizarla como sesión RPC |
| Subscribe `pane.created` y estado del panel del coordinador | ACK `subscription_started` en ambas | Suscripciones aceptadas; no llegaron eventos durante la ventana y no se afirma entrega/replay de eventos |
| Dos `send` iguales con el mismo operation ID dentro del body | Dos IDs de mensaje distintos | La API local actual no deduplica un reintento por contenido/ID de aplicación |
| Eventos de inbox de destinatario | Eventos presentes, consulta desde último cursor sin anteriores y evento nuevo posterior | Cursor de mailer útil para un consumidor durable |
| Reinicio del daemon aislado | Mismo token utilizable y mismo tail de eventos | Reanudar no exige registrar otra vez |
| Repetir registro | Nuevo ID, epoch incrementado y token viejo rechazado | Registro significa rotación, no recuperación inocua |
| Mensaje creado antes de rotación | Conserva `recipientSession`/`recipientEpoch` | El envelope mantiene su destino original |
| Git CAS con base antigua | Rechazado; rama concurrente intacta | La precondición de ref protege de avance concurrente |
| Git CAS sobre ref propia | Aceptado; `main` intacto | Mecanismo útil para integrar sin mover el checkout del usuario |
| Rust `herdr-sdk` | Compila, ping y `PaneList` tipado funcionan | Cliente de consultas viable, no certifica toda su API |
| Rust `herdr-plugin` | Compila, cliente CLI tipado lista panes | Framework utilizable para esa operación; no se ensayó su runtime de eventos |
| Go `herdr-api` | Comprueba protocolo, lista agentes y acepta suscripción del panel | Go también dispone de cliente útil; no hay ventaja exclusiva de SDK para Rust |

`protocol-probes.json` contiene 14 aserciones, todas superadas. Los contadores de recursos globales observados por los clientes se omiten del resumen persistente porque no son parte del contrato ni una medida de rendimiento.

La primera ejecución del harness de mailer buscó por error `mail.sock`; el código del producto usa `daemon.sock`. Se corrigió el harness consultando `internal/daemon/daemon.go` y se repitió con éxito. Esto no fue un fallo del producto. No se interpretó la ausencia de ese archivo supuesto como un defecto de recuperación.

## Fuentes de código que sustentan la comparación

Las revisiones completas están en [upstream-revisions.json](upstream-revisions.json). Se descargaron para lectura en un directorio temporal; no se ejecutaron sus scripts de instalación ni se lanzaron sus workers.

| Proyecto | Rutas consultadas o hallazgo determinante |
| --- | --- |
| StructuPath/herdr-swarm | README: fan-out, compatibilidad, estado reportado, harvest y CAS |
| StructuPath/herdr-conductor | README: contrato 0.7.5/protocolo 17, candidato 0.9.3, acciones atendidas y revisión por SHA |
| cyperx84/herdr-loop | README, código de motor y manifiesto: eventos, worktrees, nodos de grafo secuenciales, gates externos periódicos |
| cyperx84/herdr-api | `client.go`, `workspace.go`, README: protocolo 22, conexión por llamada, worktree.create/open y suscripciones |
| husniadil/herdr-dispatch | README y `internal/herdrclient`, `internal/spawn`: tablero/daemon/MCP, perfiles y Proxenos |
| sean1588/herdr-orchestrator | README, `internal/scheduler/scheduler.go`, `internal/store/store.go`, configuración: pipeline GitHub, polling y SQLite |
| Kampe/Herdforge | README: primitivos existentes, límites del lifecycle durable, Pi no soportado y contrato específico de launch |
| aorumbayev/herdr-workflows | README: cuatro clases de paso, comandos y retry/historial |
| Newt6611/herdr-plugin-rust | `src/client.rs`, `src/runtime.rs`, `src/events.rs`, `src/pane.rs`, Cargo.toml; consulta compilada |
| smota/meshloop | README, ADR 0022 y archivos actuales de `crates/meshloop-adapters`; integración Herdr retirada en el código actual |

Sobre Meshloop se descargó además la crate publicada `meshloop-engine` 0.1.0. Sus docs aún describen puertos Herdr y advierten que la crate es de implementación, no una API Rust estable. El árbol actual `c2cc68c0193da285735254df0914c604d36b8add` sigue una arquitectura distinta: [ADR 0022](https://github.com/smota/meshloop/blob/c2cc68c0193da285735254df0914c604d36b8add/docs/architecture/adr/0022-daemonless-context-engineering.md). La conclusión se refiere a ese árbol, no a todas sus versiones históricas.

Las alternativas genéricas se evaluaron por sus contratos primarios, no mediante instalación: [Claude Agent Teams](https://code.claude.com/docs/en/agent-teams), [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence), [Temporal Activity Execution](https://docs.temporal.io/activity-execution). `workmux` se inspeccionó en el checkout local; no se afirma que todas sus versiones posteriores tengan exactamente los mismos backends.

## Reproducción

El [probe de protocolos](protocol_probe.py) usa únicamente la biblioteca estándar de Python, Git y el binario de mailer. Crea estado privado y un repositorio en un directorio temporal; imprime resultados sin tokens ni contenido de sesiones. Requiere ejecutarse dentro de Herdr, con `HERDR_ENV=1` y su socket accesible. Usa la ruta Nix anterior por defecto; se puede proporcionar otro binario para comparar versiones:

```sh
HERDR_MAILER_BIN=/ruta/herdr-mailer python3 docs/research/supervisor/protocol_probe.py
```

No usarlo como test de CI: las consultas de Herdr son live y dependen del contexto local. Sus casos aislados de mailer/Git no prueban ejecución de modelos.

Se conservan los fuentes propios de las consultas [Rust](rust_probe.rs) y [Go](go_probe.go.txt). El archivo Go lleva `.txt` para no añadir un paquete de investigación a `go test ./...` del producto.

El experimento Rust se construyó en un directorio temporal con este manifiesto, cuyo path apunta al checkout exacto enumerado arriba:

```toml
[package]
name = "herdr-feasibility-probe"
version = "0.0.0"
edition = "2021"
publish = false

[dependencies]
herdr-sdk = "=0.0.1"
herdr-plugin = { path = "../sources/Newt6611__herdr-plugin-rust" }
serde_json = "1"
tokio = { version = "1", features = ["macros", "rt-multi-thread", "time"] }
```

El Cargo.lock exacto se conserva como [rust-probe.lock](rust-probe.lock). Para reconstruir: descargar las revisiones fijadas, copiar `rust_probe.rs` como `src/main.rs`, guardar el manifiesto y el lock como `Cargo.toml`/`Cargo.lock`, y ejecutar `cargo build --locked`. El runtime del experimento usó `herdr-sdk` 0.0.1, `herdr-plugin` 0.1.8 del checkout fijado y Tokio 1.53.2. No se ejecutó todo el test suite de esas bibliotecas.

El probe Go se compiló con Go 1.26.7 y un módulo temporal que usa `replace github.com/cyperx84/herdr-api => ../sources/cyperx84__herdr-api`, revisión `65cc5f7c10515ad5be2be55f9384cb7bd7ae970f`. El código solo abre el cliente, comprueba protocolo, lista y suscribe. Todas las cachés de estos experimentos quedaron en `/tmp`; no se instalaron binarios globales.

## Límites de la evidencia

- No se midió rendimiento, memoria ni precio de ninguna alternativa. Las preferencias de mantenimiento/lenguaje son juicios explícitos del estudio.
- No se validó un controlador completo, ni creación/retirada de worktrees con el servidor compartido durante este estudio. El diseño requiere esas pruebas futuras en un servidor/repositorio aislados.
- No se ejecutaron modelos. La evidencia anterior de Claude/Pi, sus límites de login/provider y la diferencia entre `accepted`/`channel_written` siguen en [native-runtimes.md](../../native-runtimes.md).
- No se construyó un paquete Nix del supervisor, que aún no existe. Se verifican documentos y probes, no se atribuyen los checks de mailer a un producto futuro.
- Ningún plugin comparado se certificó para despliegue general. El estudio decide encaje arquitectónico, no una auditoría exhaustiva de seguridad.

## Comprobación de entrega del estudio

| Requisito del encargo | Evidencia entregada |
| --- | --- |
| Estudiar viabilidad | Experimentos y límites anteriores; riesgos y prerrequisitos en el estudio |
| Comparar opciones | Matriz de quince opciones/familias, revisiones de diez repositorios externos y estado de proyectos locales |
| Resolver Rust frente a Go | Compilación/consultas con ambos; elección y trade-offs explícitos |
| Aprovechar Herdr nativo | Esquema, ayuda instalada, consulta real de worktrees y tabla de responsabilidades |
| Integrar mailer | Gaps reproducidos, contrato de avisos, propuesta de idempotencia y recuperación |
| Desarrollar el mejor esquema | Modelo de datos, estados, API, arranque por runtime, integración, fallos y plan de validación en el diseño |

El cierre de este estudio no da por superadas las puertas de aceptación de la implementación futura.
