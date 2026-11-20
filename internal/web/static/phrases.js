/* StudyHomie: the phrases the app says, picked at random so it does not repeat itself.
   Written in Spanish and translated with t() from i18n.js, which loads first. */
(() => {
  const P = {
    // the welcome title, by time of day
    morning: [
      t('Buenos días, ¿qué quieres entender hoy?'),
      t('¿Con qué idea arrancamos la mañana?'),
      t('Mente fresca: ¿qué tema le damos?'),
    ],
    afternoon: [
      t('¿Qué quieres entender hoy?'),
      t('¿Qué te está dando vueltas en la cabeza?'),
      t('¿Qué tema le damos esta tarde?'),
      t('Elige algo difícil: lo bajamos hasta lo básico.'),
    ],
    evening: [
      t('Buenas noches, ¿repasamos algo?'),
      t('¿Un último tema antes de dormir?'),
      t('¿Qué quieres entender esta noche?'),
    ],
    late: [
      t('¿Estudiando de madrugada? Vamos con calma.'),
      t('Insomnio productivo: ¿qué aprendemos?'),
    ],
    // while the model works
    thinking: [
      t('Pensando…'),
      t('Dándole vueltas…'),
      t('Buscando la mejor forma de explicarlo…'),
      t('Conectando ideas…'),
      t('Bajando al fondo del iceberg…'),
      t('Ordenando las ideas…'),
      t('Un momento, armando la respuesta…'),
    ],
    uploading: [
      t('Subiendo…'),
      t('Guardando tu material…'),
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
