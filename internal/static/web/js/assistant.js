// Assistant chat: posts a question to /assistant/ask and renders the grounded
// answer with its intent, latency and cited sources.
(function () {
  var chat = document.getElementById('chat');
  if (!chat) return;

  var form = document.getElementById('chat-form');
  var input = document.getElementById('chat-input');
  var send = document.getElementById('chat-send');
  var endpoint = chat.getAttribute('data-endpoint') || '/assistant/ask';

  function addBubble(role) {
    var msg = document.createElement('div');
    msg.className = 'msg msg-' + role;
    var bubble = document.createElement('div');
    bubble.className = 'bubble';
    msg.appendChild(bubble);
    chat.appendChild(msg);
    chat.scrollTop = chat.scrollHeight;
    return bubble;
  }

  function renderAnswer(bubble, data) {
    bubble.textContent = '';

    var text = document.createElement('div');
    text.className = 'answer-text';
    text.textContent = data.answer || '';
    bubble.appendChild(text);

    var meta = document.createElement('div');
    meta.className = 'answer-meta';
    if (data.intent) {
      var intent = document.createElement('span');
      intent.className = 'chip chip-intent';
      intent.textContent = data.live ? 'live: ' + data.intent : data.intent;
      meta.appendChild(intent);
    }
    if (typeof data.latency_ms === 'number') {
      var latency = document.createElement('span');
      latency.className = 'chip';
      latency.textContent = data.latency_ms + ' ms';
      meta.appendChild(latency);
    }
    bubble.appendChild(meta);

    if (data.sources && data.sources.length) {
      var sources = document.createElement('div');
      sources.className = 'sources';
      var label = document.createElement('span');
      label.className = 'sources-label';
      label.textContent = 'Sources:';
      sources.appendChild(label);
      data.sources.forEach(function (s) {
        var chip = document.createElement('span');
        chip.className = 'chip chip-src';
        chip.textContent = s;
        sources.appendChild(chip);
      });
      bubble.appendChild(sources);
    }
  }

  function ask(question) {
    var user = addBubble('user');
    user.textContent = question;

    var bot = addBubble('bot');
    bot.textContent = 'Thinking\u2026';
    input.disabled = true;
    send.disabled = true;

    var controller = new AbortController();
    var timer = setTimeout(function () { controller.abort(); }, 90000);

    fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question: question }),
      signal: controller.signal
    })
      .then(function (response) {
        return response.json()
          .catch(function () { return { error: 'Unexpected response' }; })
          .then(function (data) { return { ok: response.ok, data: data }; });
      })
      .then(function (result) {
        if (!result.ok) {
          bot.className = 'bubble bubble-error';
          bot.textContent = (result.data && result.data.error) || 'Request failed.';
          return;
        }
        bot.className = 'bubble';
        renderAnswer(bot, result.data);
      })
      .catch(function (err) {
        bot.className = 'bubble bubble-error';
        bot.textContent = (err && err.name === 'AbortError')
          ? 'The assistant took too long to respond.'
          : 'Could not reach the assistant.';
      })
      .then(function () {
        clearTimeout(timer);
        input.disabled = false;
        send.disabled = false;
        input.focus();
        chat.scrollTop = chat.scrollHeight;
      });
  }

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var question = input.value.trim();
    if (!question) return;
    input.value = '';
    ask(question);
  });
})();
