package vault

import "testing"

func TestNormalizeArea(t *testing.T) {
	cases := map[string]string{
		"":                           "",
		"   ":                        "",
		"Matemáticas":                "matematicas",
		"mathematics":                "matematicas",
		"Math":                       "matematicas",
		"Geometría":                  "matematicas",
		"Física":                     "fisica",
		"Electrónica":                "electronica",
		"circuits":                   "electronica",
		"Química":                    "quimica",
		"Biology":                    "biologia",
		"Medicina":                   "medicina",
		"Computer Science":           "informatica",
		"Ciencias de la computación": "informatica",
		"Música":                     "musica",
		"Music theory":               "musica",
		"Historia":                   "historia",
		"Historia del arte":          "arte",
		"Ciencias sociales":          "ciencias-sociales",
		"Economía":                   "economia",
		"Lenguas":                    "lenguas",
		"Filosofía":                  "filosofia",
		"音乐":                         "musica",
		"数学":                         "matematicas",
		"Algo inventado":             "general",
	}
	for in, want := range cases {
		if got := NormalizeArea(in); got != want {
			t.Errorf("NormalizeArea(%q) = %q, want %q", in, got, want)
		}
	}
}
