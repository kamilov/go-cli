# Flags и positional args

`Builder` связывает поля структуры с аргументами CLI:

- `Flag` объявляет flag с одним значением.
- `List` и `Map` объявляют повторяемые flags.
- `Arg` объявляет один positional arg, `Args` принимает оставшиеся positional args.

```go
type Options struct {
	Port    int
	Verbose int
	Color   bool
	Tag     []string
	Label   map[string]string
	Target  string
	Files   []string
}

app := cli.New("tool", func(b *cli.Builder, a *Options) {
	b.Flag(&a.Port, "port").Env("TOOL_PORT").Default(8080)
	b.Flag(&a.Verbose, "verbose", cli.Count()).Short('v')
	b.Flag(&a.Color, "color", cli.Negatable()).Default(true)
	b.List(&a.Tag, "tag")
	b.Map(&a.Label, "label")
	b.Arg(&a.Target, "target")
	b.Args(&a.Files, "file").Optional()
}, func(context.Context, *Options) error { return nil })
```

Запустите команду:

```sh
tool -vv --no-color --tag red --tag blue --label env=prod server a.txt
```

Результат разбора: `Verbose == 2`, `Color == false`, `Tag == []string{"red", "blue"}`, `Label["env"] == "prod"`, `Target == "server"`, `Files == []string{"a.txt"}`.

## Формы flags

Flag со значением принимает `--port 8080` и `--port=8080`. После `.Short('p')` доступны `-p 8080` и `-p8080`.
Короткие flags без значения можно группировать, например `-vvv`.

`bool` flag без `=...` устанавливает `true`. Для `false` используйте `--color=false`. Опция `cli.Negatable()` добавляет форму `--no-color`.
`cli.Count()` применим к `Flag[int]`: каждое вхождение увеличивает счётчик.

`List` добавляет один элемент за каждое вхождение. `Map` принимает `key=value`; повторный key перезаписывает значение.
В переменной окружения элементы `List` и пары `Map` разделяются запятыми без экранирования. Значения командной строки заменяют значения окружения целиком.

`--` завершает разбор flags. Все последующие слова становятся positional args, включая слова с начальным `-`.
Одиночный `-` тоже считается positional arg. Flag, который ожидает значение, принимает следующее слово с начальным `-`.

Повторный `Flag` перезаписывает предыдущее значение. `List` добавляет элементы, `Map` обновляет key, `Count` увеличивает счётчик.

## Источники значений

Источники значения flag по приоритету:

1. Командная строка.
2. Переменная окружения, заданная через `.Env("NAME")`.
3. Значение по умолчанию из `.Default(value)` или `.DefaultFunc(fn)`.

Пустая переменная окружения считается отсутствующей. Без источника поле сохраняет zero value.
`Required()` требует значение из командной строки или непустой переменной окружения. `Required()` нельзя применять вместе с `Default()` или `DefaultFunc()`.

`.DefaultFunc(func(ctx context.Context) (T, error))` вычисляет значение по умолчанию при каждом запуске, в котором ни командная строка, ни окружение не дали значения. Через `ctx` доступны значения родительских команд (`cli.Parent`). Flags одной команды разрешаются в порядке объявления. Справка функцию не вызывает и вычисленное значение не показывает.
`Default` и `DefaultFunc` задают одно и то же свойство: действует последний вызов.

`Arg` и `Args` читаются только из командной строки.
`Arg` обязателен по умолчанию. `.Optional()` оставляет отсутствующий аргумент незаданным, `.Default(value)` подставляет значение.
`Args` должен быть последним positional arg. Без `.Optional()` ему нужен хотя бы один элемент. После optional arg нельзя объявить required arg.

В обработчике и checks `cli.SourceOf(ctx, &field)` возвращает источник значения:

```go
source := cli.SourceOf(ctx, &a.Port)
switch source {
case cli.SourceCommandLine:
	// Значение передано в argv.
case cli.SourceEnv:
	// Значение взято из окружения.
case cli.SourceDefault:
	// Использован Default.
case cli.SourceUnset:
	// Источника нет.
}
```

`SourceOf` работает для полей выбранной команды и её предков. Для остальных полей возвращается `SourceUnset`.

## Типы и custom parser

Встроенный parser выбирается по точному типу поля: `string`, `bool`, целые типы, `float32`, `float64`, `time.Duration`.
Для `encoding.TextUnmarshaler` вызывается `UnmarshalText`. Для именованного типа без этого интерфейса задайте `.Parse(...)`.

```go
type Format string
type Options struct{ Format Format }

b.Flag(&a.Format, "format", cli.OneOf[Format]("text", "json")).
	Parse(cli.FromString[Format]).Default("text")
```

`cli.FromInt[T]` разбирает десятичные целые и проверяет диапазон типа.
Для `List` и `Args` custom parser разбирает один элемент. Для `Map` он разбирает значение после `=`.
`OneOf` доступен для `Flag` и `List` с comparable типом. `Count` применяется только к `Flag[int]`, `Negatable` только к `Flag[bool]`.

## Проверки

`Flag.Check` и `Arg.Check` получают одно значение. `List.Check`, `Map.Check` и `Args.Check` получают коллекцию целиком.
Checks значений выполняются для командной строки, окружения и default. Значение без источника пропускается.
`Builder.Check` проверяет структуру целиком, в том числе незаданные поля.

```go
b.Flag(&a.Port, "port").Check(func(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port должен быть от 1 до 65535")
	}
	return nil
})
```

Некорректный `Default` даёт ошибку схемы до `Run`. Значение из `DefaultFunc` проверяется только при запуске: ошибка функции или проверки возвращается из `Run` обычной ошибкой, не `InputError`.
При ошибке разбора переменной окружения библиотека не повторяет её значение в сообщении. Текст ошибки custom parser может содержать это значение.
