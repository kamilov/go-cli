# Быстрый старт

`cli.New[T](name, bind, run)` создает приложение. `bind` объявляет flags и positional args для полей `T`. После разбора и проверки `run` получает заполненную структуру.

```go
package main

import (
	"context"
	"fmt"
	"os"

	cli "github.com/kamilov/go-cli"
)

type Args struct {
	Output string
	Files  []string
}

func main() {
	app := cli.New("show", func(b *cli.Builder, a *Args) {
		b.Describe("Показывает список файлов")
		b.Flag(&a.Output, "output", cli.OneOf("text", "json")).
			Short('o').Default("text").Describe("Формат вывода")
		b.Args(&a.Files, "file").Describe("Файлы")
	}, func(ctx context.Context, a *Args) error {
		_, err := fmt.Fprintf(cli.Stdout(ctx), "%s: %v\n", a.Output, a.Files)
		return err
	})

	os.Exit(app.Main())
}
```

В директории проекта запустите:

```sh
go mod init example.com/show
go get github.com/kamilov/go-cli
go run . -o json a.txt b.txt
go run . --help
```

`bind` выполняется один раз при регистрации команды.
Чтобы получить ошибку сборки конфигурации до фактического запуска приложение (`Run`) вызывается метод `app.Err()`.
`Run` и `Completion` так же возвращают эту ошибку

- Подробнее о том, как работать с командами читайте в [разделе о командах](commands.md).
- Правила работы с флагами (flags) читайте в [разделе об аргументах](arguments.md).
