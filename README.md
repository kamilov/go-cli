# go-cli

`go-cli` - библиотека для создания CLI-приложений на Go.

Основные особенности:

- Generics для типизированных аргументов и опций.
- Вложенные команды.
- `bind` для объявления flags с источниками из командной строки, переменных окружения и `Default`.
- Наследование flags от родительских команд.
- Positional args, в том числе variadic.
- Completion API для интеграции с shell.

## Требования и установка

Для библиотеки нужен Go 1.27. В директории проекта запустите:

```sh
go get github.com/kamilov/go-cli
```

## Пример

```go
package main

import (
	"context"
	"fmt"
	"os"

	cli "github.com/kamilov/go-cli"
)

type Global struct{ Region string }
type Deploy struct {
	Service  string
	Replicas int
}

func main() {
	app := cli.New("tool", func(b *cli.Builder, g *Global) {
		b.Flag(&g.Region, "region").Env("TOOL_REGION").Default("ru")
	}, nil)

	app.Command("deploy", func(b *cli.Builder, d *Deploy) {
		b.Arg(&d.Service, "service")
		b.Flag(&d.Replicas, "replicas").Default(1)
	}, func(ctx context.Context, d *Deploy) error {
		g, ok := cli.Parent[Global](ctx)
		if !ok {
			return fmt.Errorf("global args not found")
		}
		_, err := fmt.Fprintf(cli.Stdout(ctx), "%s: deploy %s x%d\n", g.Region, d.Service, d.Replicas)
		return err
	})

	os.Exit(app.Main())
}
```

```text
$ tool deploy api --region us --replicas 3
us: deploy api x3
```

`--region` объявлен в корневой команде. Он доступен до и после имени подкоманды.
Без flag значение берется из `TOOL_REGION`, затем из `Default("ru")`.
Команда `deploy` объявляет positional arg `service` и flag `--replicas`.

`Main()` берет аргументы из `os.Args[1:]`, печатает ошибки в stderr и возвращает exit code.
Для запуска из Go-кода и тестов используйте `Run(ctx, args, opts...)`. В `args` не входит имя программы.

## Документация

- [Быстрый старт](docs/ru/quickstart.md): регистрация приложения и запуск.
- [Команды](docs/ru/commands.md): дерево, aliases, wrappers и доступ к flags предков.
- [Flags и positional args](docs/ru/arguments.md): типы, источники значений, проверки и custom parser.
- [Запуск и ошибки](docs/ru/runtime.md): порядок обработки, context, потоки и exit codes.
- [Help и completion](docs/ru/help-completion.md): справка, метаданные и кандидаты для shell.
- [Справочник API](docs/ru/api.md): методы и ограничения в одной таблице.

Import path: `github.com/kamilov/go-cli`. Имя пакета: `cli`.
