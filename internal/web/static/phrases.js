/* StudyHomie: the phrases the app says, picked at random so it does not repeat itself.
   One place for all of them, so they can be translated as a whole later. */
(() => {
  const P = {
    // the welcome title, by time of day
    morning: [
      'Buenos días, ¿qué quieres entender hoy?',
      '¿Con qué idea arrancamos la mañana?',
      'Mente fresca: ¿qué tema le damos?',
    ],
    afternoon: [
      '¿Qué quieres entender hoy?',
      '¿Qué te está dando vueltas en la cabeza?',
      '¿Qué tema le damos esta tarde?',
      'Elige algo difícil: lo bajamos hasta lo básico.',
    ],
    evening: [
      'Buenas noches, ¿repasamos algo?',
      '¿Un último tema antes de dormir?',
      '¿Qué quieres entender esta noche?',
    ],
    late: [
      '¿Estudiando de madrugada? Vamos con calma.',
      'Insomnio productivo: ¿qué aprendemos?',
    ],
    // while the model works
    thinking: [
      'Pensando…',
      'Dándole vueltas…',
      'Buscando la mejor forma de explicarlo…',
      'Conectando ideas…',
      'Bajando al fondo del iceberg…',
      'Ordenando las ideas…',
      'Un momento, armando la respuesta…',
    ],
    uploading: [
      'Subiendo…',
      'Guardando tu material…',
    ],
  };

  const last = {};
  // pick returns a random phrase of a kind, never the same one twice in a row
  function pick(kind) {
    const list = P[kind] || [];
    if (list.length < 2) return list[0] || '';
    let i;
    do { i = Math.floor(Math.random() * list.length); } while (i === last[kind]);
    last[kind] = i;
    return list[i];
  }

  // welcome picks the greeting for the current hour
  function welcome(now = new Date()) {
    const h = now.getHours();
    const kind = h < 5 ? 'late' : h < 12 ? 'morning' : h < 19 ? 'afternoon' : 'evening';
    return pick(kind);
  }

  // has reports whether text is one of the phrases of a kind
  const has = (kind, text) => (P[kind] || []).includes(text);

  window.Phrases = { pick, welcome, has };
})();
