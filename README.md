# StudyHomie

**Aprende con intuición y comprueba que puedes explicarlo.**
Un asistente de estudio con IA que corre en tu computadora, con una interfaz web local y un vault de notas Markdown (compatible con Obsidian) que muestra lo que has aprendido y cómo se conecta.

![Mapa de conocimiento](docs/img/mapa.png)

## Qué hace

Todo empieza en un **chat**. Pregunta lo que quieras y te responde ahí mismo; pide que te explique un tema a fondo y escribe una **guía de estudio** con sus **notas de concepto** aparte.

- **Profundidad.** Junto al cuadro de texto eliges de 0 a 4 cuántos niveles de prerrequisitos se escriben como notas propias. Con 0 sale solo la guía. Con 1, lo que el tema usa directamente. Con 4 baja hasta lo básico. También puedes decirlo con tus palabras: «desde lo más básico», «enfocado en audio».
- **Plan editable.** Antes de escribir nada te muestra el árbol de conceptos por niveles. Desmarcas lo que ya sabes (y se va con ello lo que solo dependía de eso), añades lo que falte, y decides: guía y notas, solo la guía, o cancelar. Los conceptos que ya están en tu vault se enlazan, no se reescriben.
- **Intuición sin plantillas.** Nada de «imagina que tienes…». Las guías arrancan con un problema concreto con números, te hacen reinventar la idea (el intento ingenuo, por qué falla y el arreglo), la empujan a los casos extremos, la miran desde otro ángulo y cuentan qué problema tenía quien la inventó. La fórmula llega al final, como conclusión.
- **Visuales para mover, al estilo 3Blue1Brown.** Dentro de la guía hay widgets en vivo: funciones con sliders, una matriz que deforma el plano (con î, ĵ y el determinante como área), procesos paso a paso, experimentos aleatorios que se repiten mil veces y preguntas de «predice antes de seguir leyendo». Si ninguno sirve, la guía propone una página interactiva que se construye cuando la pides.
- **Practicar.** Preguntas que piden razonar y no memorizar, explicar el tema con tus palabras (método Feynman) y tarjetas de repaso hechas con tus notas de concepto.

Desde el chat también puedes pedir el **reto del día** (un tema al azar de tus campos de interés), **planear una materia entera** (un roadmap con prerrequisitos) o **adjuntar tus apuntes** (txt, md, pdf, pptx, docx o imágenes). Los recursos externos (videos, webs, libros, papers) van siempre con su enlace, y los enlaces se comprueban antes de guardarlos.

![Nota de un tema](docs/img/nota.png)

## Requisitos

- Un navegador.
- **Una de estas dos** formas de acceder a un modelo de IA:
  - [Claude Code](https://claude.com/claude-code) instalado y con sesión iniciada (usa tu propio plan), **o**
  - una clave de la [API de Anthropic](https://console.anthropic.com/) (se paga por uso).

## Instalación

**Opción A: binario listo (sin instalar nada más).** Descarga el archivo de tu sistema desde [Releases](https://github.com/krilinxito/studyhomie/releases/latest), descomprímelo y deja `studyhomie` (o `studyhomie.exe` en Windows) donde quieras, idealmente en una carpeta que esté en tu `PATH`.

**Opción B: con Go** (1.24 o superior):

```
go install github.com/krilinxito/studyhomie/cmd/studyhomie@latest
```

**Opción C: desde el código:**

```
git clone https://github.com/krilinxito/studyhomie
cd studyhomie
go build -o studyhomie ./cmd/studyhomie
```

## Uso normal

1. **Crea una carpeta para tus estudios** y entra en ella. Ahí vivirá tu vault.

   ```
   mkdir mis-estudios
   cd mis-estudios
   ```

2. **Ejecuta `studyhomie`.** Sin argumentos abre la interfaz web en <http://127.0.0.1:8765> y crea la carpeta `vault/` la primera vez.

   ```
   studyhomie
   ```

3. **Escribe en el chat** lo que quieras entender, elige la profundidad y envía. La guía aparece en el panel de la derecha.

Para parar el programa, `Ctrl+C` en la terminal. Cada carpeta es un vault independiente: puedes tener una por materia, o una sola para todo.

> Si al arrancar ves un aviso rojo, falta configurar la IA (ver la siguiente sección).

## Interfaz

Tres columnas: tus **chats** a la izquierda, el **chat** en el centro y el **panel de estudio** a la derecha, con cuatro pestañas:

- **Guía**: la guía abierta como artículo, con sus visuales y recursos.
- **Conceptos**: las notas de concepto de la guía, por niveles, como líneas de metro. Si algo de arriba no se entiende, baja un nivel.
- **Mapa**: un plano de metro. Cada guía es una línea de color y sus conceptos son estaciones que desembocan en ella. Una guía está vacía si aún no la exploras, medio llena con la guía lista, llena si ya la comprobaste y con anillo cuando la explicaste.
- **Practicar**: preguntas, explicar con tus palabras, tarjetas de repaso y simulación.

El panel se puede ampliar u ocultar; en el móvil se ve una columna a la vez. Los chats se guardan en `vault/.studyhomie/sessions/` y se pueden **continuar**: escribe en un chat viejo y sigue donde lo dejaste, con su contexto.

Las guías y notas admiten:

- **Avisos** con la sintaxis de callouts de Obsidian (`> [!info] Definición`, `> [!example]`, `> [!tip]`, `> [!warning]`, `> [!question]`), que también se ven bien en Obsidian.
- **Tablas** y **fórmulas** (`$...$`).
- **Diagramas Mermaid** (` ```mermaid `), dibujados en local; si uno no se puede dibujar, se muestra su código.
- **Recursos en lista**, con tipo y dominio. Los videos de YouTube se reproducen dentro de la nota, pero solo cargan YouTube (`youtube-nocookie.com`) cuando haces clic en reproducir; hasta entonces no hay ninguna petición externa.

## Elegir la IA

Por defecto usa **Claude Code** (`claude-cli`). Para cambiar, ejecuta `studyhomie init` dentro de tu carpeta: crea `studyhomie.toml`, que puedes editar.

| `provider` | Qué usa | Qué necesitas |
|---|---|---|
| `claude-cli` (por defecto) | Tu instalación local de Claude Code, con tu sesión. StudyHomie no lee ni guarda tus credenciales: solo ejecuta el comando `claude`. | Instalar Claude Code y ejecutar `claude` una vez para iniciar sesión. |
| `anthropic-api` | La API de Anthropic con tu clave. | Definir la variable de entorno `ANTHROPIC_API_KEY`. |

Con la API puedes elegir modelo (`model = "claude-sonnet-5-5"`, por ejemplo). Por defecto usa `claude-opus-5-5`; un modelo más pequeño sale más barato.

Definir la clave:

```
# Windows (PowerShell), una vez; abre una terminal nueva después
setx ANTHROPIC_API_KEY "sk-ant-..."

# macOS / Linux
export ANTHROPIC_API_KEY="sk-ant-..."
```

> Revisa los términos de tu plan de Claude antes de compartir o automatizar el uso del CLI. StudyHomie lo ejecuta localmente, con tu sesión, como lo harías tú en la terminal.

## Configuración (`studyhomie.toml`)

Todo es opcional; sin el archivo se usan estos valores.

| Clave | Por defecto | Para qué |
|---|---|---|
| `provider` | `"claude-cli"` | `claude-cli` o `anthropic-api` |
| `model` | vacío | Modelo a usar; vacío = el del proveedor |
| `quick_model` | vacío | Modelo para las respuestas cortas (conceptos base y dudas); vacío = `model`. Poner uno más rápido y barato hace que respondan antes |
| `port` | `8765` | Puerto de la interfaz web |
| `language` | `"español"` | Idioma de guías, pruebas y tutor |
| `vault` | `"vault"` | Carpeta de las notas |
| `interests` | lista amplia | Campos de los que sale el reto diario. "Totalmente aleatorio" suma además los campos por defecto |
| `context_window` / `compact_at` | `200000` / `0.6` | Cuándo se resume una conversación larga para que no se acumule el contexto |
| `depth` | `1` | Profundidad de las guías que se escriben fuera del chat (roadmap, reto del día, terminal) |
| `parallel` | `3` | Notas de concepto que se escriben a la vez |

## Tu vault

Las notas son archivos Markdown normales con `[[enlaces]]`. **Abre la carpeta `vault/` con [Obsidian](https://obsidian.md)** y verás el grafo de lo que has aprendido. Un vault de una versión anterior (con `topics/`) se pasa solo a `guias/` la primera vez que arranca.

```
vault/
  guias/         un .md por tema: guía, recursos, dudas, prueba y Feynman
  conceptos/     un .md por concepto: En una frase, Intuición, Cómo funciona, Ejemplo,
                 Errores comunes, Relacionado y Aparece en; «requiere:» separa los
                 prerrequisitos de los simples enlaces
  roadmaps/      el plan de cada roadmap
  daily/         un registro por día
  interactive/   simulaciones generadas (HTML)
  .studyhomie/   estado interno (resúmenes entre fases, apuntes subidos, sesiones guardadas)
```

## Desde la terminal

Todo lo que hace la interfaz web también se puede hacer sin ella:

```
studyhomie roadmap "Teoría de juegos"     # alcance + grafo de temas
studyhomie material apuntes.pdf           # roadmap desde tus apuntes
studyhomie next teoría-de-juegos          # siguiente tema cuyos prerrequisitos ya dominas
studyhomie daily                          # reto del día (o: daily Física, daily todos)
studyhomie study "Teorema de Bayes"       # preparación → prueba → Feynman
studyhomie guide "Transformada de Fourier" --depth 2 --foco "audio"
studyhomie prepare | quiz | feynman | extend | interactive <tema>
studyhomie doubt <tema> "tu pregunta"     # responde una duda y la guarda en el artículo
studyhomie graph                          # temas y conexiones
studyhomie serve --port 9000 --no-open    # interfaz web en otro puerto, sin abrir el navegador
studyhomie help
```

## Privacidad y seguridad

- **Qué se envía al modelo:** el tema que estudias, tus respuestas y explicaciones, y los fragmentos de tus apuntes que sean relevantes. Tus notas y tu vault se quedan en tu computadora.
- **La interfaz web solo escucha en `127.0.0.1`.** Rechaza peticiones con un `Host` que no sea local y exige `Origin` propio en todo lo que modifica algo, de modo que otra página abierta en tu navegador no puede usarla. La única página externa que puede incrustar es el reproductor de YouTube, y solo después de que pulses reproducir.
- **Las simulaciones las escribe la IA**, así que se sirven aisladas: sin red, sin acceso a la app ni a tus datos, y se rechazan las que intenten cargar recursos externos.

## Desarrollo

```
go test ./...
```

Estructura: `cmd/studyhomie` (entrada), `internal/session` (las tres fases y los flujos), `internal/provider/*` (backends de IA, intercambiables), `internal/vault` (notas y grafo), `internal/web` (servidor e interfaz), `internal/material` (extracción y búsqueda en apuntes), `internal/roadmap` (validación del grafo).

Para publicar una versión basta con subir una etiqueta (`git tag v0.2.0 && git push --tags`): el workflow de GitHub compila los binarios y crea la release.

## Licencia

[MIT](LICENSE)
