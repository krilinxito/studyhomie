package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/providers"
	"github.com/krilinxito/studyhomie/internal/session"
	"github.com/krilinxito/studyhomie/internal/sources"
	"github.com/krilinxito/studyhomie/internal/update"
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/web"
)

const usage = `StudyHomie — aprende con intuición y explicando.

Uso (sin argumentos abre la interfaz web):
  studyhomie serve [--port N] [--no-open]
                                        interfaz web: grafo, notas y sesiones
  studyhomie init [--provider P] [--model M] [--quick-model M] [--base-url URL] [--vault DIR] [--port N]
                                        crea studyhomie.toml y el vault (con flags, sin preguntar nada;
                                        sobre un archivo existente cambia solo esos valores)
  studyhomie check [--live]             la configuración, el vault y qué IAs están listas;
                                        --live le manda un mensaje de prueba (sale con 1 si falla)
  studyhomie version
  studyhomie update [--check] [-y]      busca una versión nueva y, si la hay, pregunta si instalarla;
                                        --check solo avisa, -y instala sin preguntar
  studyhomie roadmap <materia>          te pregunta el alcance, traza la ruta de temas y escribe la guía del 1.º
  studyhomie material <archivo>...      roadmap a partir de txt, md, pdf, pptx, docx o imágenes
  studyhomie next <roadmap>             el tema en el que vas; al aprobarlo se escribe la guía del siguiente
  studyhomie daily [campo|todos]        reto del día de tus campos de interés ("todos": cualquier campo)
  studyhomie study <tema>               preparación → prueba → Feynman
  studyhomie prepare <tema>             solo la guía de estudio
  studyhomie guide <tema> [--depth N] [--foco "..."]
                                        guía + notas de concepto hasta N niveles de prerrequisitos
  studyhomie quiz <tema> [n] [dificultad]
  studyhomie feynman <tema>
  studyhomie extend <tema>              añade los conceptos base que la guía da por sabidos
  studyhomie doubt <tema> "pregunta"    responde una duda y la guarda en la sección Dudas de la nota
  studyhomie interactive <tema>         genera una simulación interactiva del tema
  studyhomie graph                      temas y conexiones del vault
  studyhomie migrate-sources            migra los recursos externos de guías viejas a fuentes/
`

// version is set at release time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

// exe is the running program, which an update replaces.
var exe = update.Executable()

// errRestart asks main to start the program again on the version just installed.
var errRestart = errors.New("restart")

func main() {
	update.Cleanup(exe)                 // the .exe an earlier update moved aside
	cmd, args := "serve", []string(nil) // running the program with no arguments opens the web UI
	if len(os.Args) > 1 {
		cmd, args = os.Args[1], os.Args[2:]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "version", "-v", "--version":
		fmt.Println("studyhomie", version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, cmd, args)
	stop()
	if errors.Is(err, errRestart) {
		fmt.Println("Reiniciando en la versión nueva…")
		err = update.Restart(exe)
		if err != nil {
			err = fmt.Errorf("la versión nueva quedó instalada, pero no pude reiniciar (%w): vuelve a abrir StudyHomie", err)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "init":
		return initProject(args, os.Stdout)
	case "check":
		return check(ctx, args, os.Stdout)
	case "update":
		return selfUpdate(ctx, args, bufio.NewReader(os.Stdin), os.Stdout)
	}
	cfg, err := config.Load(config.FileName)
	if err != nil {
		return err
	}
	v, err := vault.Open(cfg.Vault)
	if err != nil {
		return err
	}
	if cmd == "graph" {
		return printGraph(v)
	}
	if cmd == "migrate-sources" {
		n, err := v.MigrateSources()
		if err != nil {
			return err
		}
		fmt.Printf("Listo: %d guías migradas a fuentes/.\n", n)
		return nil
	}
	p, err := providers.New(cfg)
	if err != nil {
		return err
	}
	if cmd == "serve" {
		srv := web.New(cfg, v, p)
		srv.Warning = providers.Check(cfg)
		srv.ConfigPath = config.FileName
		srv.Updater = update.New(version, exe)
		if srv.Warning != "" {
			fmt.Fprintln(os.Stderr, "Aviso:", srv.Warning)
		}
		return serve(ctx, cfg, v, srv, args)
	}
	s := &session.Session{P: p, V: v, Cfg: cfg, Sources: sources.New(cfg.YouTubeKey, cfg.BraveKey), In: bufio.NewReader(os.Stdin), Out: os.Stdout}

	if cmd == "daily" {
		field := strings.Join(args, " ")
		if strings.EqualFold(field, "todos") {
			field = session.AllFields
		}
		return s.Daily(ctx, field)
	}
	if len(args) == 0 {
		return fmt.Errorf("falta el argumento\n\n%s", usage)
	}
	topic := args[0]
	switch cmd {
	case "roadmap":
		rslug, err := s.Roadmap(ctx, strings.Join(args, " "), session.RoadmapOpts{})
		if err != nil {
			return err
		}
		return startFirst(ctx, s, rslug)
	case "material":
		rslug, err := s.Material(ctx, args, session.RoadmapOpts{})
		if err != nil {
			return err
		}
		return startFirst(ctx, s, rslug)
	case "next":
		return s.Next(ctx, vault.Slugify(strings.Join(args, " ")))
	case "study":
		return s.Study(ctx, strings.Join(args, " "), "adhoc")
	case "prepare":
		_, err := s.Prepare(ctx, strings.Join(args, " "), "adhoc")
		return err
	case "guide":
		req, err := guideArgs(args, cfg.Depth)
		if err != nil {
			return err
		}
		_, err = s.Guide(ctx, req)
		return err
	case "extend":
		text, err := s.Extend(ctx, vault.Slugify(strings.Join(args, " ")))
		if err != nil {
			return err
		}
		fmt.Println(text)
		return nil
	case "doubt":
		if len(args) < 2 {
			return fmt.Errorf("uso: studyhomie doubt <tema> \"pregunta\"\n\n%s", usage)
		}
		ans, err := s.Doubt(ctx, vault.Slugify(topic), strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		fmt.Println(ans.Entry)
		for _, c := range ans.Concepts {
			fmt.Printf("Podría ser su propio tema: %s\n", c.Title)
		}
		return nil
	case "interactive":
		return s.Interactive(ctx, vault.Slugify(strings.Join(args, " ")), "")
	case "quiz":
		n, diff := 5, "media"
		if len(args) > 1 {
			if n, err = strconv.Atoi(args[1]); err != nil {
				return fmt.Errorf("n debe ser un número: %w", err)
			}
		}
		if len(args) > 2 {
			diff = args[2]
		}
		return s.Quiz(ctx, vault.Slugify(topic), n, diff)
	case "feynman":
		return s.Feynman(ctx, vault.Slugify(topic))
	}
	return fmt.Errorf("comando desconocido %q\n\n%s", cmd, usage)
}

// guideArgs reads "tema... [--depth N] [--foco texto...]".
func guideArgs(args []string, depth int) (session.GuideReq, error) {
	req := session.GuideReq{Depth: depth, Source: "adhoc"}
	var topic, focus []string
	into := &topic
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--depth", "--profundidad":
			if i+1 >= len(args) {
				return req, errors.New("--depth necesita un número")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 || n > session.MaxDepth {
				return req, fmt.Errorf("profundidad no válida: %q (0 a %d)", args[i+1], session.MaxDepth)
			}
			req.Depth = n
			i++
		case "--foco", "--focus":
			into = &focus
		default:
			*into = append(*into, args[i])
		}
	}
	req.Topic, req.Focus = strings.Join(topic, " "), strings.Join(focus, " ")
	if strings.TrimSpace(req.Topic) == "" {
		return req, errors.New("falta el tema")
	}
	return req, nil
}

// serve runs the local web UI until interrupted.
func serve(ctx context.Context, cfg config.Config, v *vault.Vault, srv *web.Server, args []string) error {
	// after an update the browser tab is already open and reloads by itself
	port, open := cfg.Port, os.Getenv(update.RestartedEnv) == ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-open":
			open = false
		case "--port":
			if i+1 >= len(args) {
				return errors.New("--port necesita un número")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("puerto no válido: %q", args[i+1])
			}
			port = n
			i++
		default:
			return fmt.Errorf("opción desconocida %q", args[i])
		}
	}
	ln, err := srv.Listen(port)
	for tries := 0; err != nil && !open && tries < 50; tries++ {
		time.Sleep(200 * time.Millisecond) // a restart: the old process may still hold the port
		ln, err = srv.Listen(port)
	}
	if err != nil {
		return fmt.Errorf("no pude abrir el puerto %d (¿ya hay otro StudyHomie corriendo? prueba --port): %w", port, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	fmt.Printf("StudyHomie en %s  (vault: %s, IA: %s)\nCtrl+C para salir.\n", url, v.Root, cfg.Provider)
	if open {
		openBrowser(url)
	}

	restart := make(chan struct{}, 1)
	srv.Restart = func() {
		select {
		case restart <- struct{}{}:
		default:
		}
	}
	watch, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go srv.WatchUpdates(watch)

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	stopServer := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(shutdown)
	}
	select {
	case <-ctx.Done():
		stopServer()
		return nil
	case <-restart:
		stopServer()
		return errRestart
	case err := <-errc:
		return err
	}
}

// selfUpdate is "studyhomie update": it looks for a newer release and installs it if you agree.
func selfUpdate(ctx context.Context, args []string, in *bufio.Reader, out io.Writer) error {
	checkOnly, yes := false, false
	for _, a := range args {
		switch a {
		case "--check":
			checkOnly = true
		case "-y", "--yes":
			yes = true
		default:
			return fmt.Errorf("opción desconocida %q", a)
		}
	}
	u := update.New(version, exe)
	if !u.Enabled() {
		fmt.Fprintf(out, "Esta es una compilación de desarrollo (%s): no se actualiza sola.\n", version)
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	st := u.Check(cctx, true)
	cancel()
	if st.Error != "" && !st.Available {
		return errors.New(st.Error)
	}
	if !st.Available {
		fmt.Fprintf(out, "Tienes la última versión (%s).\n", version)
		return nil
	}
	if st.Staged {
		fmt.Fprintf(out, "La versión %s ya está instalada: se usa la próxima vez que abras StudyHomie.\n", st.Latest)
		return nil
	}
	fmt.Fprintf(out, "Hay una versión nueva: %s (tienes %s).\n", st.Latest, version)
	if notes := strings.TrimSpace(st.Notes); notes != "" {
		lines := strings.Split(notes, "\n")
		if len(lines) > 20 {
			lines = append(lines[:20], "…")
		}
		fmt.Fprintf(out, "\n%s\n\n", strings.Join(lines, "\n"))
	}
	if st.Page != "" {
		fmt.Fprintf(out, "Cambios: %s\n", st.Page)
	}
	if checkOnly {
		fmt.Fprintln(out, "Instálala con: studyhomie update")
		return nil
	}
	if !yes {
		fmt.Fprintf(out, "¿Actualizar a %s? [s/N] ", st.Latest)
		line, _ := in.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "s" && a != "si" && a != "sí" && a != "y" && a != "yes" {
			fmt.Fprintln(out, "No se actualizó.")
			return nil
		}
	}
	fmt.Fprintln(out, "Descargando y verificando…")
	tag, err := u.Install(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Listo: StudyHomie %s quedó instalado. Ábrelo de nuevo para usarlo.\n", tag)
	return nil
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start() // best effort: the URL is printed anyway
}

// startFirst offers to begin the freshly created roadmap right away.
func startFirst(ctx context.Context, s *session.Session, rslug string) error {
	fmt.Println()
	return s.Next(ctx, rslug)
}

func printGraph(v *vault.Vault) error {
	nodes, edges, err := v.Graph()
	if err != nil {
		return err
	}
	fmt.Printf("%d temas, %d conexiones\n", len(nodes), len(edges))
	for _, n := range nodes {
		fmt.Printf("- %s [%s, dominio %d]\n", n.Slug, n.Meta.Status, n.Meta.Mastery)
	}
	for _, e := range edges {
		fmt.Printf("  %s -> %s\n", e.From, e.To)
	}
	return nil
}
