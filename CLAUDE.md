# CLAUDE.md

StudyHomie es un asistente de estudio con IA: un binario de Go que trae una interfaz web local y guarda todo en un vault de Markdown compatible con Obsidian. El usuario le pide en un chat lo que quiere entender. La app escribe una guía, mapea los conceptos que esa guía da por sabidos (el «iceberg»), arma roadmaps de materias enteras y lo hace practicar con preguntas y con el método Feynman.

## Comandos

```sh
go build -o studyhomie.exe ./cmd/studyhomie   # binario (Windows)
go test ./...                                 # tests
go vet ./...
gofmt -l .                                    # CI falla si lista algo
node --check internal/web/static/app.js       # sintaxis del frontend (no tiene build ni tests JS)
```

**Después de cada cambio, recompila el exe** de la raíz del repo con `go build -o studyhomie.exe ./cmd/studyhomie`, para que el binario esté siempre al día con el código. Si la app está corriendo, ciérrala antes, porque Windows no deja sobrescribir un `.exe` en uso.

Para probarla a mano: `studyhomie init --provider claude-cli` en una carpeta temporal y después `studyhomie serve --port 8799 --no-open`. El frontend está embebido (`//go:embed static`), así que después de tocar `internal/web/static/` hay que **recompilar y reiniciar** el servidor.

## Estructura

| Paquete | Qué hace |
|---|---|
| `cmd/studyhomie` | CLI: `serve`, `init`, `check`, `update`, `roadmap`, `material`, `next`, `study`, `guide`, `quiz`, `feynman`… La lista completa está en `usage`, en `main.go`. |
| `internal/session` | Todos los flujos con la IA: chat (`chat.go`), guía + plan de conceptos (`guide.go`), roadmap (`roadmap.go`), material (`material.go`), quiz/Feynman (`session.go`), dudas/base (`extend.go`), reto del día (`daily.go`), simulaciones (`interactive.go`). |
| `internal/roadmap` | Valida el plan de una materia (orden topológico, sin ciclos) y decide qué tema está desbloqueado (`Steps`, `Next`, `Blocker`, `Passed`). |
| `internal/vault` | Lectura y escritura de notas (frontmatter YAML + cuerpo Markdown), secciones `## …`, conceptos, apuntes y búsqueda. |
| `internal/prompts` | Prompts embebidos (`*.md`). `Get(name, lang)` reemplaza `{{language}}` y, en los prompts que lee el estudiante, agrega `style.md`. |
| `internal/provider/*`, `internal/providers` | Backends de IA intercambiables (Claude CLI, Codex, APIs, modelos locales) y su catálogo. |
| `internal/web` | Servidor HTTP + SSE (`run.go`: un flujo a la vez, eventos `out`/`ask`/`card`/`done`), API JSON (`server.go`, `settings.go`, `updates.go`…) y frontend en `static/`. |
| `internal/viz` | Valida y limpia los widgets ` ```viz ` de las guías. |
| `internal/update` | Autoactualización desde GitHub Releases. |
| `internal/config` | `studyhomie.toml` (`Default()` tiene los valores por defecto). |
| `internal/i18n` | Idiomas: catálogos, `T`/`Sprintf`, encabezados de sección en cada idioma. |

## Modelo de datos (vault)

- `guias/<slug>.md`: una guía o un tema de roadmap. Los campos de `vault.Meta` son `status` (`new` → `prepared` → `tested` → `explained`), `mastery` (0–100), `roadmap`, `order`, `block`, `concepts`, `pending` (el iceberg que todavía no tiene nota)…
- `conceptos/<id>.md`: notas de concepto (`vault.Concept`).
- `roadmaps/<slug>.md`: la nota de la ruta, con `reach` (cuánto abarca) y `guide_depth`.
- `apuntes/`, `daily/`, `interactive/`; y `.studyhomie/` (oculto para Obsidian): handoff de cada guía, material fuente, sesiones del chat.
- El código busca secciones por su título en español (`## Guía`, `## Conexiones`, `## Dudas`, `## Prueba`…). No las renombres sin migrar las notas.

## Dos profundidades

- **Profundidad de guía** (`GuideReq.Depth`, 0–4, selector «Escribir ahora» del chat): cuántos niveles de conceptos previos se escriben como notas. El planner (`prompts/plan.md`) mapea hasta lo básico (`maxMapDepth`), y lo que queda más abajo de `Depth` va a `pending`. Es **aglomerativo**: junta todo lo que el tema da por sabido.
- **Profundidad de roadmap** (`reach`, 1–3: enfocado / con bases / desde cero): cuánto del campo abarca la ruta. El roadmap es **lineal**: una secuencia numerada. Solo se escribe la guía del tema 1. El tema N+1 se desbloquea cuando el N tiene `mastery ≥ pass_score` (70 por defecto, en Ajustes) con estado `tested` o `explained`. En ese momento `Session.advance` escribe su guía. `Guide()` rechaza los temas bloqueados (`checkUnlocked`), y las guías de roadmap no piden aprobar el plan (`GuideReq.Auto`).

## Idiomas (i18n)

La interfaz y la IA hablan español, inglés, francés, chino (simplificado) y portugués de Brasil (`internal/i18n`, `Langs`). Lo elige `language` en `studyhomie.toml` o Ajustes → Idioma. Si está vacío, la primera vez se toma el idioma del navegador.

- **El español es el idioma fuente y la clave.** Cada mensaje se escribe en español y su texto exacto es la clave en `internal/i18n/locales/{en,fr,zh,pt}.json`; el español no tiene catálogo. Si un mensaje no está en un catálogo, se muestra en español.
- **Go:** `s.printf`, `s.termf`, `s.status`, `s.readLine`, `s.confirm`, `s.tr` y `s.trf` traducen su formato solos. Los errores que ve el usuario se crean con `inputErrorf`/`failf` y se traducen al mostrarlos (`ErrorText`). Un texto definido lejos de donde se muestra (una lista de opciones, un mapa) se marca con `i18n.N("…")`. Los prompts reciben `s.aiLang()`.
- **Web:** `/i18n.js` (servido por Go antes que todo) define `window.I18N` y las funciones `t('texto {var}', { var })` y `tn(n, 'singular', 'plural')`. En `viz.js` se llama `tr()`, porque ahí `t` es una variable local. Los textos fijos de `index.html` llevan `data-i18n` (o `data-i18n-title`, `-placeholder`, `-aria-label`). Los errores del servidor llegan en español y `api()` los traduce si los conoce.
- **Secciones de las notas:** `i18n.Heading(lang, "Guía")` da el encabezado en el idioma activo. `vault.Section`/`ReplaceSection` y `sectionOf`/`stripSection` en JS encuentran la sección en cualquiera de los cinco idiomas, así que siempre se buscan por su nombre en español.
- **Preguntas del flujo:** `Session.Ask` recibe la etiqueta en español. La web la manda traducida en `label` y en español en `key`, y `app.js` decide qué botones mostrar mirando `key`.
- **Test:** `TestCatalogsCoverEveryMessage` (`internal/i18n`) junta con el AST de Go y regex de JS todos los mensajes marcados, y falla si a algún catálogo le falta uno, si sobra alguno o si cambian los marcadores (`%s`, `{name}`). Con `I18N_DUMP=<dir> go test ./internal/i18n` escribe en `<dir>/<idioma>.json` lo que falta traducir. **Al agregar o cambiar un texto, agrega su traducción en los cuatro catálogos.**

## Convenciones

- **Idioma del código:** los comentarios, los nombres y los prompts van en **inglés**. Los mensajes al usuario se escriben en **español** y se traducen (ver «Idiomas»).
- **Consola vs. web:** `Session.printf` sale en los dos, `termf` solo en la consola (rutas, avisos técnicos, convenciones como «.» o `/fin`) y `status` va al indicador de trabajo. Cuando hay UI (`Session.Card != nil`), lo estructurado sale como **tarjeta** (`card.go`: `question`, `plan`, `progress`, `guide`, `score`, `roadmap`…) en lugar de texto, y `app.js` la dibuja en `renderCard`.
- **Errores del usuario:** `inputErrorf(...)` (la web responde 400 y el chat lo muestra tal cual); el resto son fallas.
- **Tests:** la IA se reemplaza con `fake` (en `session`) o `scripted` (en `web`), que devuelven respuestas en orden, así que **cada llamada a la IA consume una respuesta**. Si un flujo agrega una llamada (por ejemplo, la guía 1 al crear un roadmap), hay que agregar su respuesta en los tests. `newSession` usa `Depth=0` e `Iceberg=false` para que no haya llamada de plan.
- **Frontend:** `app.js` es un script clásico (no un módulo) con funciones globales: `h()`, `icon()`, `api()`, `show(panel)`, `openNote()`, `chatAction()`, `renderCard()`, y el estado global en `S`. Los paneles usan dockview. Los iconos son sprites de `icons.svg` (`icon('lock')`). Los colores salen de variables CSS con tema claro y oscuro.
- **Saltos de línea:** el repo es LF (`.gitattributes`). En Windows, si editas con Python, abre los archivos con `newline=''`; si no, quedan en CRLF y `gofmt -l` los marca.
- **Commits:** convencionales (`feat:`, `fix:`, `docs:`…), porque Release Please arma la versión y el changelog a partir de ellos.

## Verificar en el navegador

Los cambios de UI se prueban en un navegador de verdad, no alcanza con los tests. En esta máquina: `playwright-core` con `chromium.launch({ channel: 'msedge' })`, porque está Edge y no Chrome (`chromium-cli` en npm es un paquete falso). Como las funciones de `app.js` son globales, `page.evaluate(() => renderCard({...}, run))` simula tarjetas sin llamar a la IA. Siembra un vault escribiendo los `.md` directamente.

## Plan de trabajo

`plan/PLAN.md` (en `.gitignore`) tiene las fases de la v0.3. Se trabaja una fase por sesión y se marca ✅ al terminar.
