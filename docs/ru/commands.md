# Команды

`cli.New` создаёт корневую команду приложения.
`app.Command(...)` добавляет дочернюю команду и возвращает её узел (`*cli.Command`).
Тип структуры аргументов выводится из `bind` или `run`. Если обе функции равны `nil`, укажите `cli.NoArgs` явно.

```go
type Global struct{ Region string }
type Deploy struct{ Service string }

app := cli.New("tool", func(b *cli.Builder, g *Global) {
	b.Describe("Управление сервисами")
	b.Flag(&g.Region, "region").Default("eu")
}, nil)

app.Command("deploy", func(b *cli.Builder, d *Deploy) {
	b.Describe("Развернуть сервис")
	b.Alias("dp")
	b.Arg(&d.Service, "service")
}, func(ctx context.Context, d *Deploy) error {
	g, ok := cli.Parent[Global](ctx)
	if !ok {
		return fmt.Errorf("нет аргументов корневой команды")
	}
	_, err := fmt.Fprintf(cli.Stdout(ctx), "%s: %s\n", g.Region, d.Service)
	return err
})
```

Все три вызова передают обработчику одинаковые значения:

```sh
tool --region us deploy api
tool deploy api --region us
tool dp api --region us
```

Flags предков действуют во всём поддереве. `cli.Parent[T](ctx)` возвращает структуру ближайшего предка с типом `T`. Вне вызова `Run` или `Complete` возвращается `false`.

Узел с дочерними командами не может объявлять positional args. Если у узла нет `run`, вызов без дочерней команды вернёт `*cli.InputError`. `--help` на таком узле работает.

Для группы без собственных данных используйте `app.Command[cli.NoArgs]("group", nil, nil)`.

## Описание и видимость

`Builder.Describe` задаёт текст help. Первая строка показывается в списке команд.

- `Alias(names...)` добавляет альтернативные имена.
- `Hidden()` скрывает команду из help, подсказок при опечатке и completion. Запуск по имени сохраняется.
- `Deprecated(message)` тоже скрывает команду из списков. При запуске сообщение выводится в stderr.

Aliases доступны только подкомандам. Имена соседних команд и aliases должны быть различны. Ошибки регистрации доступны через `app.Err()`.

## Wrappers и проверки

`Builder.Wrap` принимает `func(context.Context, cli.Next) error`. Wrapper действует на команду и её поддерево.
Wrappers вызываются от корня к выбранной команде. `next(ctx)` продолжает цепочку. Без вызова `next(ctx)` обработчик не запускается.

```go
app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
	b.Wrap(func(ctx context.Context, next cli.Next) error {
		start := time.Now()
		err := next(ctx)
		fmt.Fprintf(cli.Stderr(ctx), "elapsed: %s\n", time.Since(start))
		return err
	})
}, nil)

app.Command("status", nil, func(ctx context.Context, _ *cli.NoArgs) error {
	_, err := fmt.Fprintln(cli.Stdout(ctx), "ok")
	return err
})
```

Для проверки нескольких полей используйте `Builder.Check(func(ctx context.Context) error)`.
Поля доступны через замыкание, аргументы предка через `cli.Parent[T](ctx)`.
Checks выполняются после разбора значений и до wrappers. Для одной команды можно задать один wrapper и несколько checks.
