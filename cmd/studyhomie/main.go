package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	"github.com/krilinxito/studyhomie/internal/vault"
	"github.com/krilinxito/studyhomie/internal/web"
)

const usage = `StudyHomie — aprende con intuición y explicando.

Uso (sin argumentos abre la interfaz web):
  studyhomie serve [--port N] [--no-open]
                                        interfaz web: grafo, notas y sesiones
  studyhomie init                       crea studyhomie.toml para cambiar la configuración
  studyhomie roadmap <materia>          te pregunta el alcance y traza el grafo de temas
  studyhomie material <archivo>...      roadmap a partir de txt, md, pdf, pptx, docx o imágenes
  studyhomie next <roadmap>             siguiente tema cuyos prerrequisitos ya dominas
  studyhomie daily [campo|todos]        reto del día de tus campos de interés ("todos": cualquier campo)
  studyhomie study <tema>               preparación → prueba → Feynman
  studyhomie prepare <tema>             solo la guía de estudio
  studyhomie quiz <tema> [n] [dificultad]
  studyhomie feynman <tema>
  studyhomie extend <tema>              añade los conceptos base que la guía da por sabidos
  studyhomie doubt <tema> "pregunta"    responde una duda y la guarda en la sección Dudas de la nota
  studyhomie interactive <tema>         genera una simulación interactiva del tema
  studyhomie graph                      temas y conexiones del vault
`

// version is set at release time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
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
	defer stop()
	if err := run(ctx, cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	if cmd == "init" {
		return initProject()
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
	p, err := providers.New(cfg)
	if err != nil {
		return err
	}
	if cmd == "serve" {
		srv := web.New(cfg, v, p)
		srv.Warning = providers.Check(cfg)
		if srv.Warning != "" {
			fmt.Fprintln(os.Stderr, "Aviso:", srv.Warning)
		}
		return serve(ctx, cfg, v, srv, args)
	}
	s := &session.Session{P: p, V: v, Cfg: cfg, In: bufio.NewReader(os.Stdin), Out: os.Stdout}

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
		rslug, err := s.Roadmap(ctx, strings.Join(args, " "))
		if err != nil {
			return err
		}
		return startFirst(ctx, s, rslug)
	case "material":
		rslug, err := s.Material(ctx, args)
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

// serve runs the local web UI until interrupted.
func serve(ctx context.Context, cfg config.Config, v *vault.Vault, srv *web.Server, args []string) error {
	port, open := cfg.Port, true
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
	if err != nil {
		return fmt.Errorf("no pude abrir el puerto %d (¿ya hay otro StudyHomie corriendo? prueba --port): %w", port, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	fmt.Printf("StudyHomie en %s  (vault: %s, IA: %s)\nCtrl+C para salir.\n", url, v.Root, cfg.Provider)
	if open {
		openBrowser(url)
	}

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(shutdown)
		return nil
	case err := <-errc:
		return err
	}
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

func initProject() error {
	if _, err := os.Stat(config.FileName); err == nil {
		return fmt.Errorf("%s ya existe", config.FileName)
	}
	cfg := config.Default()
	if err := config.Write(config.FileName, cfg); err != nil {
		return err
	}
	if _, err := vault.Open(cfg.Vault); err != nil {
		return err
	}
	fmt.Printf("Listo: %s y vault en ./%s (puedes abrirlo con Obsidian)\n", config.FileName, cfg.Vault)
	return nil
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
