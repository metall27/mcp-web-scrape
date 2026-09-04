# Инструкция: снятие эталона шрифтовых метрик с реального десктопа (#107, ступень C)

Эталон (`fontmetrics_win.json`) — таблица ширин/высот, снятая с **реального
десктопного Chrome**. Ступень C (мок `measureText` / `offsetWidth` /
`FontFaceSet` в контейнере) будет подменять ответы контейнерного Chromium
этой таблицей. От качества снятия зависит весь мок.

Утилита снятия: `docs/tools/desktop-font-dump.html` (эта же ветка/PR).
Захватывает: `measureText`-ширины (7 probe-строк × 5 стилей × 82 семейства,
включая probe-строку челленджа Ozon `mmmwwwmmmWWW`), `spanProbe`
(offsetWidth/offsetHeight @72px — реплика фонт-зонда `script_v47_4.js`),
`installed` (список FontFace'ов для `getUserFonts`-мока).

Каждый шаг обязателен. Не пропускай проверки — плохой эталон тихо обнулит
всю ступень C.

---

## Шаг 0. Узнай версию Chromium в контейнере

```bash
ssh fb-u26 "docker exec mcp-web-scrape chromium-browser --version"
# ожидаем на текущем образе: Chromium 149.0.7827.53 Alpine Linux
```

Нужна **мажорная+build-линия** (`149.0.7827`); последний патчик (.53 vs .155)
на метрики не влияет — тот же рендерер. Если в контейнере другая версия —
дальше везде подставляй свою линию (см. Шаг 1, п. 4).

## Шаг 1. Скачай Chrome for Testing нужной версии

Обычный установочный Chrome текущей ветки (Stable 152+) НЕ подходит — нужна
та же линия, что в контейнере. Google публикует автономные сборки
«Chrome for Testing» (CfT) — portable, без установки, не трогает твой
основной браузер.

1. Windows x64 — скачать и распаковать:
   https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.155/win64/chrome-win64.zip
   Распаковать, например, в `C:\cft\` → бинарь `C:\cft\chrome-win64\chrome.exe`.
2. macOS (Intel) —
   https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.155/mac-x64/chrome-mac-x64.zip
3. macOS (Apple Silicon) —
   https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.155/mac-arm64/chrome-mac-arm64.zip
4. Если линия в контейнере не 149.0.7827 — найди свой последний патч:
   https://googlechromelabs.github.io/chrome-for-testing/latest-patch-versions-per-build.json
   (ключ вида `"149.0.7827"` → `"version": "149.0.7827.155"`), затем подставь
   его в шаблон:
   `https://storage.googleapis.com/chrome-for-testing-public/<version>/<platform>/chrome-<platform>.zip`
   Каталог всех платформ: https://googlechromelabs.github.io/chrome-for-testing/

## Шаг 2. Подготовь окружение снятия

1. **Windows: масштаб 100%.** Параметры → Система → Дисплей → Масштаб = 100%
   (при 125%/150% offsetWidth снимется в масштабированных единицах — эталон
   будет мусором).
2. Закрой лишние приложения (тайминги и половина width-колонки чувствительны
   к load — не критично, но зачем шум).
3. Никаких шрифтовых пакетов/антидетект-браузеров на этой машине не ставь —
   эталон должен отражать штатный набор ОС.

## Шаг 3. Запусти CfT с чистым профилем

Windows (PowerShell):
```powershell
C:\cft\chrome-win64\chrome.exe --user-data-dir=C:\cft\profile --no-first-run --no-default-browser-check about:blank
```

macOS (терминал):
```bash
 unzip chrome-mac-arm64.zip   # если ещё не распакован
 ./chrome-mac-arm64/Google\ Chrome\ for\ Testing.app/Contents/MacOS/Google\ Chrome\ for\ Testing \
   --user-data-dir=/tmp/cft-profile --no-first-run --no-default-browser-check about:blank
```

Проверь версию в запущенном браузере: `chrome://version` → строка
`Google Chrome for Testing 149.0.7827.155` (линия должна совпадать с Шагом 0).

## Шаг 4. Открой dump-утилиту

1. Возьми `docs/tools/desktop-font-dump.html` из этой ветки (после влития PR —
   из master). Скопируй файл на машину снятия.
2. В запущенном CfT: Ctrl/Cmd+O → выбери файл (откроется как
   `file:///.../desktop-font-dump.html`). Только Chrome — не Safari/Firefox.
3. Дождись готовности: текст `<pre>` перестаёт быть `RUNNING...`, становится
   JSON'ом. Контроль: DevTools (F12) → Elements → `<pre id="out" ...>` —
   атрибут должен быть `data-done="1"`. Обычно готово за секунды; если
   открыл без DevTools — просто подожди 3–5 секунд.

## Шаг 5. Сохрани дамп

1. Кликни в `<pre>`, выдели всё (Ctrl/Cmd+A), скопируй (Ctrl/Cmd+C).
2. Вставь в текстовый редактор **как обычный текст** (не в богатый —
   Word/заметки испортят кавычки), сохрани в UTF-8:
   - Windows → `fontmetrics_win.json`
   - macOS → `fontmetrics_mac.json`

## Шаг 6. Проверь дамп (обязательно)

Сохрани как `check_fontdump.py` рядом с JSON и запусти:

```bash
python3 check_fontdump.py fontmetrics_win.json
```

```python
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
```

Критерии прохождения — ВСЕ одновременно:
- `os: Win32` (для mac-эталона — `MacIntel`);
- `ua ok: True`;
- три assert'а молча пройдены (Verdana≠Tahoma, Impact≠Comic Sans,
  baseline на месте);
- `collision groups` — единицы (пары типа Segoe UI == Segoe UI Semibold —
  норма, они реально делят метрики);
- `installed` — число больше 20;
- `probe key present: True`.

Любой провал → переснятие (чаще всего: не тот браузер, масштаб ≠ 100%,
обрезанный при копировании JSON).

## Шаг 7. Положи эталон в репозиторий

```bash
git checkout master && git pull origin master
git checkout -b feat/107-stage-c-font-mock
cp <путь к fontmetrics_win.json> internal/pkg/browser/fontmetrics_win.json
git add internal/pkg/browser/fontmetrics_win.json
git commit -m "feat(#107): stage C — desktop font metrics reference (Win32, CfT 149.0.7827.155)"
git push origin feat/107-stage-c-font-mock
```

Открыть PR в master с `Related #107` в описании (НЕ `Fixes` — issue
закрывается только после прохождения контрольного прогона ozon).
Мак-эталон — `fontmetrics_mac.json` туда же, отдельным коммитом.

## Шаг 8 (опционально). Сними контейнерный дамп для диффа

```bash
curl -s -o /tmp/dump.html <ссылка raw на desktop-font-dump.html из master>
scp /tmp/dump.html fb-u26:/tmp/
ssh fb-u26 "docker cp /tmp/dump.html mcp-web-scrape:/tmp/dump.html && \
  docker exec mcp-web-scrape chromium-browser --headless --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --dump-dom file:///tmp/dump.html 2>/dev/null" \
  > /tmp/container_dump.html
```

Вытащить JSON из `<pre id="out">` и прогон `check_fontdump.py` по нему
**должен упасть** на `Verdana==Tahoma` — это и есть палевные коллизии,
которые мок ступени C обязан закрыть. Приложи оба файла к issue #107 —
имплементатору мока понадобится дифф.

---

## Частые ошибки

- Снял в Safari/Firefox/основном Chrome другой версии — метрики другие.
- Windows-масштаб ≠ 100% при снятии.
- Антидетект-браузер (Mimic/Latte и пр.) — их шрифтовые моки попадут в
  эталон вместо реальной системы.
- JSON скопирован не весь (pre обрезался при выделении) — Шаг 6 отловит.
- Открыл dump.html до того, как дождался `data-done="1"` — срежет хвост.
