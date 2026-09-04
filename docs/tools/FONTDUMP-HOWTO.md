# Инструкция: снятие эталона шрифтовых метрик с реального десктопа (#107, ступень C)

Эталон (`fontmetrics_<os>.json`) — таблица ширин/высот, снятая с **реального
десктопного Chrome**. Ступень C (мок `measureText` / `offsetWidth` /
`FontFaceSet` в контейнере) будет подменять ответы контейнерного Chromium
этой таблицей. От качества снятия зависит весь мок.

Утилита снятия: `docs/tools/desktop-font-dump.html`. Она захватывает:

- `fonts.check` + `measureText`-ширины (6 probe-строк × 5 стилей × 82 семейства),
  включая probe-строку челленджа Ozon `mmmwwwmmmWWW`;
- `spanProbe` — offsetWidth/offsetHeight @72px для всех семейств: точная реплика
  фонт-зонда `script_v47_4.js` (fab_chlg);
- `installed` — список FontFace'ов в `document.fonts` (для `getUserFonts`-мока).

## 1. Подготовка машины (важно — эталон должен быть «чистым десктопом»)

1. **ОС: Windows в приоритете** (прод-профиль контейнера — Windows-стек:
   WebGL мокается под ANGLE/D3D11, UA — Windows Chrome). macOS — вторым
   номером, если профиль MacIntel будет использоваться отдельно.
2. Chrome той же мажорной версии, что Chromium в контейнере
   (`docker exec mcp-web-scrape chromium-browser --version` — сверить
   мажорную цифру; эталон снимается для конкретной линейки рендерера).
3. **Чистый профиль**: запусти Chrome с отдельным profile-dir, без расширений:
   - Windows: `chrome.exe --user-data-dir=C:\temp\fontdump-profile`
   - macOS: `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=/tmp/fontdump-profile`
4. Закрой все лишние вкладки (тайминги дампа чувствительны к нагрузке,
   а версионные поля UA не должны отличаться).
5. **Не ставь** никаких шрифтовых пакетов/антидетект-браузеров поверх —
   эталон должен отражать штатный набор системы.

## 2. Снятие дампа

1. Скачай свежий `docs/tools/desktop-font-dump.html` из master
   (репозиторий mcp-web-scrape, ветка master, коммит `1c393a3` или новее —
   в старых нет `spanProbe` и probe-строки `mmmwwwmmmWWW`).
2. Открой файл **локально** в подготовленном Chrome:
   - Windows: двойной клик по файлу или `file:///C:/path/to/desktop-font-dump.html`;
   - macOS: перетащи файл в окно Chrome (не в Safari!).
3. Дождись, пока `<pre>` перестанет показывать `RUNNING...` и в нём появится
   JSON, а атрибут тега станет `data-done="1"` (DevTools → Elements → pre#out).
   Скрипт синхронный — обычно готов мгновенно, но дай 2–3 секунды.
4. Выдели ВЕСЬ текст `<pre>` (клик в него → Ctrl/Cmd+A → Ctrl/Cmd+C) и сохрани
   в файл `fontmetrics_win.json` (для macOS — `fontmetrics_mac.json`).

## 3. Проверка дампа (обязательная — до коммита)

Открой сохранённый JSON и проверь:

1. **Поля-заголовки**:
   - `"os"` — `Win32` для Windows (для mac — `MacIntel`);
   - `"ua"` — десктопный Chrome, версия совпадает с контейнерным Chromium
     по мажорной цифре.
2. **spanProbe** (ключевая секция для fab_chlg):
   - есть ключ `br0k3nd3f4u17` (базлайн-фолбэк);
   - значения `Verdana` ≠ `Tahoma` (на реальном Windows они различаются —
     это главный маркер, по которому мы сейчас палимся);
   - `Impact` ≠ `Comic Sans MS`; `Consolas` ≠ `Courier New`;
   - максимум 3–4 группы коллизий на 82 семейства (пары типа
     `Segoe UI`==`Segoe UI Semibold` — норма, они реально делят метрики).
3. **installed** — непустой список (десятки записей `Family|style|weight`).
   Если `[]` или `"ERR ..."` — что-то пошло не так, пересними.
4. **widths** — для каждого семейства 30 записей (6 проб × 5 стилей);
   probe-ключ `16px|mmmwwwmmmWWW` присутствует.

Быстрый sanity-скрипт (можно прогнать на любой машине с Python):

```bash
python3 - <<'EOF'
import json, sys
d = json.load(open(sys.argv[1] if len(sys.argv) > 1 else 'fontmetrics_win.json'))
print('os:', d['os'])
print('ua ok:', 'Chrome/' in d['ua'])
sp = d.get('spanProbe')
assert isinstance(sp, dict) and len(sp) >= 80, 'spanProbe missing/short'
assert 'br0k3nd3f4u17' in sp, 'baseline missing'
assert sp['Verdana'] != sp['Tahoma'], 'FAIL: Verdana==Tahoma (desktop never does this)'
assert sp['Impact'] != sp['Comic Sans MS'], 'FAIL: Impact==Comic Sans'
coll = {}
for fam, v in sp.items():
    coll.setdefault(v, []).append(fam)
multi = {k: v for k, v in coll.items() if len(v) > 1}
print('collision groups:', len(multi))
for v in list(multi.values())[:10]: print('  =='.join(v))
inst = d.get('installed')
print('installed:', len(inst) if isinstance(inst, list) else inst)
f = d['fonts']['Arial']['widths']
print('probe key present:', '16px|mmmwwwmmmWWW' in f, '| width:', f.get('16px|mmmwwwmmmWWW'))
EOF
```

Ожидаемый вывод: `os: Win32`, `ua ok: True`, assertion'ы молча пройдены,
`collision groups` — единицы, `installed` — число > 20.

## 4. Куда положить

Путь по договорённости (упомянут в самом dump.html):
`internal/pkg/browser/fontmetrics_win.json` — рядом с будущим кодом мока
(пакет `browser` — там stealth-инъекции; мок будет читать файл через
`go:embed`). Мак-эталон — `fontmetrics_mac.json` там же.

Коммит в master обычным путём (issue #107, ступень C):

```
git checkout -b feat/107-stage-c-font-mock
cp <путь к снятому файлу> internal/pkg/browser/fontmetrics_win.json
git add internal/pkg/browser/fontmetrics_win.json
git commit -m "feat(#107): stage C — desktop font metrics reference (Win32, Chrome <major>)"
```

PR с `Related #107` (НЕ `Fixes` — issue закрывается только после прохождения
контрольного прогона ozon).

## 5. Сверка с контейнером (опционально, но полезно)

После коммита эталона сними тот же дамп с контейнера и сравни:

```bash
# дамп из контейнера (fb-u26)
curl -s https://raw.githubusercontent.com/metall27/mcp-web-scrape/master/docs/tools/desktop-font-dump.html -o /tmp/dump.html
scp /tmp/dump.html fb-u26:/tmp/
ssh fb-u26 "docker cp /tmp/dump.html mcp-web-scrape:/tmp/dump.html && \
  docker exec mcp-web-scrape chromium-browser --headless --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --dump-dom file:///tmp/dump.html 2>/dev/null" > /tmp/container_dump.html
```

Различия desktop-vs-container по spanProbe — это ровно те подмены, которые
должен делать мок ступени C. Файл `/tmp/container_dump.html` можно приложить
к issue #107 для удобства имплементации.

## Частые ошибки

- **Снял в Safari/Firefox** — метрики другие, эталон непригоден. Только Chrome.
- **Открыл dump.html по http(s)** — подойдёт, но file:// проще и без CSP-шумов.
- **Снял с антидетект-браузером** (Mimic/Latte и пр.) — их шрифтовые моки
  попадут в эталон вместо реальной системы. Только штатный Chrome.
- **Скопировал JSON не весь** (pre обрезался при выделении) — проверка п.3
  отловит (widths будет < 30 записей).
- **Windows: снял под RDP с нестандартным DPI** — масштабирование может
  повлиять на offsetWidth. Снимай при 100% масштабе (Параметры → Дисплей →
  Масштаб 100%).
