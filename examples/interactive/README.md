# Interactive Actions Examples

Примеры использования интерактивных действий в `scrape_with_js` для работы с login-protected контентом, динамическими элементами и SPA.

## Базовые действия

### 1. Login на сайте

```json
{
  "url": "https://example.com/login",
  "actions": [
    {
      "type": "type",
      "selector": "#username",
      "text": "myuser"
    },
    {
      "type": "type",
      "selector": "#password",
      "text": "mypass"
    },
    {
      "type": "click",
      "selector": "button[type='submit']"
    },
    {
      "type": "wait_for_text",
      "text": "Welcome",
      "timeout": 10000
    }
  ]
}
```

### 2. Работа с фильтрами

```json
{
  "url": "https://shop.example.com/products",
  "actions": [
    {
      "type": "scroll_to",
      "selector": "#filters"
    },
    {
      "type": "click",
      "selector": "button[data-filter='price']"
    },
    {
      "type": "wait_for",
      "selector": ".products-grid",
      "timeout": 5000
    }
  ]
}
```

### 3. Lazy loading контент

```json
{
  "url": "https://news.example.com",
  "actions": [
    {
      "type": "scroll_to",
      "selector": "footer"
    },
    {
      "type": "wait_for",
      "selector": ".article:nth-child(10)",
      "timeout": 5000
    }
  ]
}
```

## Продвинутые действия

### 4. Dropdown меню

```json
{
  "url": "https://example.com/form",
  "actions": [
    {
      "type": "hover",
      "selector": ".dropdown-menu"
    },
    {
      "type": "wait_for",
      "selector": ".dropdown-menu.open",
      "timeout": 2000
    },
    {
      "type": "click",
      "selector": ".dropdown-menu .item:first-child"
    }
  ]
}
```

### 5. Заполнение сложной формы

```json
{
  "url": "https://example.com/registration",
  "actions": [
    {
      "type": "type",
      "selector": "#name",
      "text": "John Doe"
    },
    {
      "type": "type",
      "selector": "#email",
      "text": "john@example.com"
    },
    {
      "type": "select_option",
      "selector": "#country",
      "value": "United States"
    },
    {
      "type": "click",
      "selector": "#terms-checkbox"
    },
    {
      "type": "submit",
      "selector": "form"
    }
  ]
}
```

### 6. Выполнение JavaScript

```json
{
  "url": "https://example.com/page",
  "actions": [
    {
      "type": "execute_js",
      "text": "window.scrollTo(0, document.body.scrollHeight);"
    },
    {
      "type": "wait_for",
      "selector": ".lazy-loaded-content",
      "timeout": 3000
    }
  ]
}
```

## Комбинированные примеры

### 7. E-commerce поиск и фильтрация

```json
{
  "url": "https://shop.example.com",
  "actions": [
    {
      "type": "type",
      "selector": "#search",
      "text": "laptop"
    },
    {
      "type": "click",
      "selector": "button[type='submit']"
    },
    {
      "type": "wait_for",
      "selector": ".search-results",
      "timeout": 5000
    },
    {
      "type": "scroll_to",
      "selector": "#filters"
    },
    {
      "type": "click",
      "selector": "input[name='price_range']"
    },
    {
      "type": "wait_for_text",
      "text": "Showing",
      "timeout": 3000
    }
  ]
}
```

### 8. Прокрутка и подгрузка контента

```json
{
  "url": "https://social.example.com/feed",
  "actions": [
    {
      "type": "execute_js",
      "text": "window.scrollBy(0, 500);"
    },
    {
      "type": "wait_for",
      "selector": ".post:nth-child(5)",
      "timeout": 3000
    },
    {
      "type": "execute_js",
      "text": "window.scrollBy(0, 500);"
    },
    {
      "type": "wait_for",
      "selector": ".post:nth-child(10)",
      "timeout": 3000
    }
  ]
}
```

## Параметры действий

### Общие параметры:

- `type` (обязательный): Тип действия
- `selector`: CSS selector для элемента
- `timeout`: Timeout в миллисекундах (дефолт: 30000)
- `retries`: Количество ретраев (дефолт: 3)

### Специфические параметры:

**Для `type`:**
- `text`: Текст для ввода

**Для `select_option`:**
- `value`: Значение опции

**Для `wait_for_text`:**
- `text`: Текст для ожидания

**Для `execute_js`:**
- `text`: JavaScript код

**Для `upload_file`:**
- `text`: Путь к файлу

## Доступные типы действий

### Базовые (Priority: High):
- `click` — кликнуть по элементу
- `type` — ввести текст в поле
- `submit` — отправить форму
- `scroll_to` — прокрутить к элементу
- `wait_for` — ждать появления элемента

### Продвинутые (Priority: Medium):
- `wait_for_text` — ждать текста на странице
- `hover` — навести мышь (для dropdowns)
- `select_option` — выбрать в dropdown
- `execute_js` — выполнить JS код
- `upload_file` — загрузить файл

## Комбинация с опциями скрапера

Интерактивные действия можно комбинировать с другими опциями:

```json
{
  "url": "https://example.com/login",
  "stealth_enabled": true,
  "stealth_scroll": true,
  "wait_for_network_idle": true,
  "output_format": "markdown",
  "actions": [
    {
      "type": "type",
      "selector": "#username",
      "text": "user"
    },
    {
      "type": "type",
      "selector": "#password",
      "text": "pass"
    },
    {
      "type": "click",
      "selector": "button[type='submit']"
    }
  ]
}
```

## Важно

1. **Кэширование:** Запросы с действиями не кэшируются
2. **Stealth режим:** Действия автоматически получают случайные задержки если stealth включен
3. **Retry логика:** Каждое действие ретраится до 3 раз при ошибке
4. **Timeout:** У каждого действия есть timeout 30 секунд по умолчанию
5. **Логирование:** Каждое действие подробно логируется

## Метаданные результата

При выполнении действий в метаданные возвращается информация:

```json
{
  "_metadata": {
    "interactive_actions": {
      "count": 3,
      "action_types": ["type", "type", "click"],
      "cached": false
    }
  }
}
```