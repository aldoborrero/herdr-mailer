# Evidencia del estudio de Herdr Projects

Fecha: 2026-10-04. Estudio y pruebas aisladas; no despliegue. La conclusión está en [herdr-projects-assessment.md](../../herdr-projects-assessment.md).

## Revisión y alcance

- Upstream: [eliasstravik/herdr-projects](https://github.com/eliasstravik/herdr-projects/tree/4e4548c3c43888e6be1c96906215998f07dc63f0), SHA `4e4548c3c43888e6be1c96906215998f07dc63f0`, manifiesto 0.2.34. `git ls-remote ... HEAD` devolvió esa revisión.
- Checkout de pruebas: un directorio temporal (`$REVIEW_CHECKOUT` en los logs); cambios limitados al módulo de pruebas independiente y la sustitución de la ruta de `kill`.
- Artefactos Cargo: un directorio temporal (`$CARGO_TARGET_DIR` en los logs). `cargo test --locked` utiliza Cargo.lock upstream.
- Mailer: main `903df1d` al comienzo; se consultaron el registro/autorización, suscripciones NATS, entrega nativa y launcher. No se cambió su implementación.
- Herdr: `herdr --version` devuelve `herdr 0.9.1`. `herdr machine add --help` confirma target SSH, label y `--remote-session`; no se llamó a `machine add`.
- Fuentes web adicionales: [máquinas Herdr](https://herdr.dev/docs/connecting-machines/), [NetBird SSH](https://docs.netbird.io/manage/peers/ssh), consultadas el 4 de octubre. Son documentación actual, no prueba de nuestra configuración de red.

No se ejecutaron instaladores, configuradores globales ni modelos. Las pruebas unitarias usan `World`, `FakeRunner` y directorios temporales; los tests CLI upstream ejecutan el binario con entorno aislado. Ninguna prueba de este estudio certifica dos hosts reales o comportamiento de un modelo.

## Resultados preservados

| Archivo | Ejecución | Resultado |
| --- | --- | --- |
| [upstream-tests.log](upstream-tests.log) | `cargo test --locked`, upstream sin modificaciones | 324 unit tests OK, 1 FAILED: `runner::tests::group_kill_reaches_grandchildren`. Los 4 CLI no se ejecutan porque la fase anterior falla |
| [remote-scheduler.log](remote-scheduler.log) | Prueba independiente del ticker completo | Reproduce exclusión del segundo proyecto: 65 ticks, 17 consultas SSH del primer proyecto, ninguna del segundo |
| [patched-tests.log](patched-tests.log) | Suite con siete pruebas independientes y parche de portabilidad | 332 unit tests OK + 4 CLI OK = 336 |
| [coordinator_review.rs](coordinator_review.rs) | Siete pruebas de caracterización | Afirmaciones de comportamiento observado, incluidas limitaciones; PASS no significa corrección del problema |
| [review-module.patch](review-module.patch) | Añade el módulo de pruebas a `main.rs` | No cambia comportamiento de producción |
| [nixos-kill.patch](nixos-kill.patch) | Sustituye dos usos de `/bin/kill` por `kill` | Prueba de portabilidad, no paquete Nix ni instalación. Un paquete definitivo debe garantizar qué ejecutable resuelve |

La suite upstream usa un proceso hijo/grupo desechable para probar cancelación. Se comprobó que `/bin/kill` no existe en este host y que `type -P kill` resuelve a coreutils en `/nix/store`. Después del parche temporal pasa también esa prueba. No se creó ningún `/bin/kill` ni se cambió el host.

## Pruebas independientes

| Nombre, prefijo `review_` | Preparación y observación | Interpretación |
| --- | --- | --- |
| `capacity_is_advisory_and_identical_starts_allocate_distinct_threads` | Límite 1; dos solicitudes iguales; dos IDs y dos creaciones de worktree | Capacidad orientativa y ausencia de deduplicación por operación |
| `replacing_native_session_does_not_change_thread_match` | Mismos pane/cwd/kind/nombre; otra sesión y terminal | La asociación del thread no fija identidad nativa/epoch |
| `remote_branch_probe_timeout_is_reported_as_absent` | Runner SSH devuelve timeout | `branch_exists` devuelve false, sin propagar estado desconocido |
| `followup_can_be_delivered_before_its_durable_record_fails` | Runtime simulado acepta prompt; una carpeta ocupa la ruta task.md e impide reemplazarla | La entrega puede preceder a una escritura fallida; no hay outbox en ese camino |
| `first_project_monopolizes_polling_of_a_shared_remote_machine` | Alpha activo y beta pendiente, ambos en box, worktrees/panes distintos; ticker completo 65 veces | Solo se consultan informes de alpha; beta conserva 0 intentos de arranque |
| `corrupt_thread_is_omitted_from_inventory` | Se corrompe el TOML de un thread en estado temporal | `load` falla, `list` lo omite |
| `restart_refuses_an_ambiguous_preexisting_branch` | Registro fallido sin ruta; la rama ya existe | Negativa segura: exige inspección del worktree parcial |

El ensayo de reparto pasa por `ticker::tick_for_test`, que llama al `tick` de producción, con tiempo lógico avanzado por sus pasadas. No duerme 65 ciclos reales ni simula que esa duración equivalga a una medición de latencia. La comprobación inicial por pasos se reforzó con este ensayo del bucle completo antes del cierre.

## Cómo repetir

Desde un checkout de mailer con estos artefactos:

```sh
mailer_study_dir="$PWD/docs/research/projects-depth"
projects_review_dir="$(mktemp -d -t projects-review.XXXXXX)"
git clone https://github.com/eliasstravik/herdr-projects.git "$projects_review_dir/upstream"
git -C "$projects_review_dir/upstream" checkout --detach 4e4548c3c43888e6be1c96906215998f07dc63f0
cd "$projects_review_dir/upstream"
cargo test --locked
```

La primera ejecución puede fallar según la existencia de `/bin/kill`. Para añadir las reproducciones y comprobar la corrección de portabilidad, conservando primero ese resultado:

```sh
cp "$mailer_study_dir/coordinator_review.rs" src/coordinator_review.rs
git apply "$mailer_study_dir/review-module.patch"
cargo test --locked coordinator_review -- --nocapture
git apply "$mailer_study_dir/nixos-kill.patch"
cargo test --locked
```

Se requiere toolchain Rust compatible y las herramientas de los tests upstream. La revisión temporal no debe ejecutarse contra sesiones o proyectos reales. Las pruebas no aplican una corrección al reparto remoto: reproducen su comportamiento para que una implementación posterior tenga un caso de aceptación concreto que invertir.

## Mapa de evidencia de implementación

Las rutas siguientes pertenecen al SHA upstream fijado; se pueden abrir desde el árbol enlazado arriba.

| Pregunta | Rutas y funciones inspeccionadas |
| --- | --- |
| Quién planifica y quién ejecuta | `skill/COORDINATOR.md`, `skill/THREAD.md`, `src/coordinator.rs`, `src/threads.rs::start` |
| Qué persiste y con qué atomicidad | `src/project.rs::write_atomic/lock`, `src/thread.rs::allocate/update/list`, `src/inbox.rs::write` |
| Qué cuenta como worker | `src/thread.rs::agent_matches/live_state`, `src/herdr.rs::Agent/AgentSession` |
| Qué sucede tras crear worktree | `src/threads.rs::place_and_brief/finish_placement/restart_plan/restart` |
| Entrega y reintento | `src/brief.rs::claim/deliver/step/bump`, `src/threads.rs::prompt`, `src/steps.rs::nudge` |
| Control de procesos y timeouts | `src/runner.rs::RealRunner/kill`, `src/ticker.rs::start/run/launch_pass` |
| Máquina y perfil remoto | `src/remote.rs::ssh_target/resolve_profile/repo_info/write_brief`, `src/herdr.rs::on_machine/cmd`, `src/profiles.rs` |
| Polling, fallos y recuperación | `src/ticker.rs::tick/tick_slow/remote_pass/brief_pass`, `src/steps.rs::Memory/machine_is_due/record_machine` |
| Copia e integridad de resultados | `src/remote.rs::layout/fetch_file/fetch_dir`, `src/thread.rs::copy_home_remote/brief_for` |
| Cierre e integración | `src/pr.rs::check`, `src/steps.rs::resolve_merged/auto_resolve`, `src/threads.rs::resolve/clean/delete_branch` |
| Backlog, capacidad y decisiones | `src/tasks.rs`, `src/assign.rs`, `src/settings.rs`, `src/threads.rs::start`, `skill/COORDINATOR.md` |
| Empaquetado y entorno | `Cargo.toml`, `Cargo.lock`, `build.rs`, `src/main.rs::extend_path`, `src/paths.rs::config_dir`, `src/remote.rs::HP_PATH` |

No se hizo una auditoría exhaustiva de seguridad, rendimiento, memoria o de todas las carreras posibles. Los resultados positivos de mocks no verifican compatibilidad con un servidor remoto, shell, configuración SSH o sesión de agente concreta. Los criterios pendientes para despliegue están enumerados como fases en el estudio, no como trabajo ya ejecutado.
