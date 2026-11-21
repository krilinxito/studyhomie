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
    // Narval companion quips (concise, warm, never spammy)
    pet: [
      t('Si algo suena difícil, lo bajamos un nivel en el iceberg.'),
      t('Un paso pequeño hoy también protege tu racha.'),
      t('Explicarlo con tus palabras vale más que releer tres veces.'),
      t('¿Probamos un visual interactivo o unas tarjetas?'),
      t('Aquí estoy por si te atascas con algún concepto.'),
    ],
    goalMet: [
      t('¡Meta diaria cumplida! Tu racha está a salvo hoy.'),
      t('¡Objetivo de hoy listo! Buen ritmo.'),
    ],
    guideReady: [
      t('¡Guía lista! Si algo da por sabido demasiado, mira el iceberg.'),
      t('Ya la tienes armada. Prueba un visual o las actividades al final.'),
    ],
    cardCollected: [
      t('¡Qué joya para tu colección!'),
      t('¡Esa no era fácil! Directa al álbum.'),
    ],
    activitiesDone: [
      t('¡Te pasaste todas las actividades de esta guía!'),
      t('De la teoría a la práctica: impecable.'),
    ],
    goodScore: [
      t('¡Lo tienes clarísimo! Se nota que lo entendiste de verdad.'),
      t('Gran resultado. Así se afianza un tema.'),
    ],
    lowScore: [
      t('Buen primer intento: ahí es justo donde más se aprende.'),
      t('Tranquilo, ahora sabes exactamente qué pieza reforzar.'),
    ],
    atRisk: [
      t('Tu racha espera un pequeño repaso antes de que acabe el día.'),
      t('Un par de tarjetas o una lectura rápida y salvas la racha.'),
    ],
    lostStreak: [
      t('Hoy empezamos una racha nueva. Sin presión.'),
    ],
    wakeUp: [
      t('¡Volviste! ¿Retomamos donde lo dejamos?'),
    ],
    error: [
      t('¡Uy! Algo tropezó con la IA. Probamos de nuevo cuando quieras.'),
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
