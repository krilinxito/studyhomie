package vault

import "strings"

// Areas are the canonical knowledge-area identifiers stored in a note's frontmatter
// (`area:`) so widgets and cross-language progress stats share one stable taxonomy.
var Areas = []string{
	"matematicas",
	"fisica",
	"electronica",
	"quimica",
	"biologia",
	"medicina",
	"informatica",
	"musica",
	"historia",
	"ciencias-sociales",
	"economia",
	"lenguas",
	"arte",
	"filosofia",
	"general",
}

var areaSet = func() map[string]bool {
	m := make(map[string]bool, len(Areas))
	for _, a := range Areas {
		m[a] = true
	}
	return m
}()

// areaAliases maps common names across languages and subfields to a canonical area.
var areaAliases = map[string]string{
	// math
	"matematica": "matematicas", "matematicas": "matematicas",
	"math": "matematicas", "maths": "matematicas", "mathematics": "matematicas",
	"mathematiques": "matematicas", "geometria": "matematicas", "geometry": "matematicas",
	"algebra": "matematicas", "calculo": "matematicas", "calculus": "matematicas",
	"estadistica": "matematicas", "statistics": "matematicas", "probabilidad": "matematicas",
	"probability": "matematicas", "trigonometria": "matematicas", "aritmetica": "matematicas",
	// physics
	"fisica": "fisica", "physics": "fisica", "physique": "fisica",
	"mecanica": "fisica", "mechanics": "fisica", "optica": "fisica", "optics": "fisica",
	"termodinamica": "fisica", "astronomia": "fisica", "astronomy": "fisica",
	"astrofisica": "fisica", "cuantica": "fisica", "relatividad": "fisica",
	// electronics
	"electronica": "electronica", "electronics": "electronica", "electronique": "electronica",
	"circuitos": "electronica", "circuits": "electronica", "electricidad": "electronica",
	"electricity": "electronica", "ingenieria-electrica": "electronica",
	"electrical-engineering": "electronica", "robotica": "electronica", "robotics": "electronica",
	"senales": "electronica", "signals": "electronica",
	// chemistry
	"quimica": "quimica", "chemistry": "quimica", "chimie": "quimica",
	"bioquimica": "quimica", "biochemistry": "quimica",
	// biology
	"biologia": "biologia", "biology": "biologia", "biologie": "biologia",
	"genetica": "biologia", "genetics": "biologia", "ecologia": "biologia",
	"ecology": "biologia", "botanica": "biologia", "zoologia": "biologia",
	"microbiologia": "biologia", "neurociencia": "biologia", "neuroscience": "biologia",
	// medicine
	"medicina": "medicina", "medicine": "medicina", "medecine": "medicina",
	"salud": "medicina", "health": "medicina", "anatomia": "medicina", "anatomy": "medicina",
	"fisiologia": "medicina", "physiology": "medicina", "farmacologia": "medicina",
	// computer science
	"informatica": "informatica", "computacion": "informatica",
	"ciencias-de-la-computacion": "informatica", "computer-science": "informatica",
	"cs": "informatica", "computing": "informatica", "informatique": "informatica",
	"programacion": "informatica", "programming": "informatica", "software": "informatica",
	"algoritmos": "informatica", "algorithms": "informatica", "redes": "informatica",
	"networking": "informatica", "ciberseguridad": "informatica", "cybersecurity": "informatica",
	"inteligencia-artificial": "informatica", "machine-learning": "informatica",
	"ai": "informatica", "ia": "informatica", "sistemas": "informatica",
	// music
	"musica": "musica", "music": "musica", "musique": "musica",
	"teoria-musical": "musica", "music-theory": "musica", "armonia": "musica",
	"harmony": "musica", "solfeo": "musica", "acustica": "musica",
	// history
	"historia": "historia", "history": "historia", "histoire": "historia",
	"arqueologia": "historia", "archaeology": "historia",
	// social sciences
	"ciencias-sociales": "ciencias-sociales", "social-sciences": "ciencias-sociales",
	"social": "ciencias-sociales", "sciences-sociales": "ciencias-sociales",
	"sociologia": "ciencias-sociales", "sociology": "ciencias-sociales",
	"psicologia": "ciencias-sociales", "psychology": "ciencias-sociales",
	"geografia": "ciencias-sociales", "geography": "ciencias-sociales",
	"politica": "ciencias-sociales", "politics": "ciencias-sociales",
	"derecho": "ciencias-sociales", "law": "ciencias-sociales",
	"antropologia": "ciencias-sociales", "anthropology": "ciencias-sociales",
	// economics
	"economia": "economia", "economics": "economia", "economie": "economia",
	"finanzas": "economia", "finance": "economia", "contabilidad": "economia",
	// languages
	"lenguas": "lenguas", "lenguaje": "lenguas", "idiomas": "lenguas",
	"languages": "lenguas", "language": "lenguas", "langues": "lenguas",
	"linguistica": "lenguas", "linguistics": "lenguas", "gramatica": "lenguas",
	"literatura": "lenguas", "literature": "lenguas", "escritura": "lenguas",
	// art
	"arte": "arte", "art": "arte", "artes": "arte", "arts": "arte",
	"historia-del-arte": "arte", "art-history": "arte", "diseno": "arte",
	"design": "arte", "arquitectura": "arte", "architecture": "arte",
	"dibujo": "arte", "pintura": "arte", "fotografia": "arte", "cine": "arte",
	// philosophy
	"filosofia": "filosofia", "philosophy": "filosofia", "philosophie": "filosofia",
	"etica": "filosofia", "ethics": "filosofia", "logica": "filosofia", "logic": "filosofia",
	// general
	"general": "general", "other": "general", "otro": "general", "otros": "general", "misc": "general",
}

// NormalizeArea turns a free-form or localized area label into one of Areas.
// Empty input returns "". Any non-empty unrecognized label becomes "general".
func NormalizeArea(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	key := foldArea(s)
	if areaSet[key] {
		return key
	}
	if a, ok := areaAliases[key]; ok {
		return a
	}
	// check Chinese / CJK common area words before folding dropped them
	for tok, a := range cjkAreaAliases {
		if strings.Contains(s, tok) {
			return a
		}
	}
	// try matching individual words when the model returned a compound like "historia y geografia"
	for _, part := range strings.Split(key, "-") {
		if a, ok := areaAliases[part]; ok {
			return a
		}
	}
	return "general"
}

var cjkAreaAliases = map[string]string{
	"数学": "matematicas", "物理": "fisica", "电子": "electronica", "电路": "electronica",
	"化学": "quimica", "生物": "biologia", "医学": "medicina", "计算机": "informatica",
	"编程": "informatica", "音乐": "musica", "历史": "historia", "社会": "ciencias-sociales",
	"经济": "economia", "语言": "lenguas", "艺术": "arte", "哲学": "filosofia",
}

func foldArea(s string) string {
	s = strings.ToLower(foldAccents(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// AreaList returns the canonical area names formatted for prompts.
func AreaList() string { return strings.Join(Areas, " | ") }
