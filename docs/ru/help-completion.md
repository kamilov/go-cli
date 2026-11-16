# Help и completion

`--help` и `-h` доступны каждой команде. `Builder.Version("1.2.3")` на корне добавляет `--version`.
Help показывает usage, дочерние команды, positional args, собственные и унаследованные flags.
Для help подкоманды запустите `tool command --help`.

```go
app := cli.New("tool", func(b *cli.Builder, _ *cli.NoArgs) {
	b.Describe("Инструмент для задач")
	b.Version("1.2.3")
}, nil)
```

`Builder.HelpFunc` на корне заменяет встроенный renderer. Функция получает `io.Writer` текущего запуска и `cli.CommandInfo` выбранной команды.
`app.Info()` возвращает снимок метаданных дерева без значений текущего запуска. `CommandInfo` содержит путь, описание, flags, positional args и дочерние команды.

```go
b.HelpFunc(func(w io.Writer, info cli.CommandInfo) error {
	_, err := fmt.Fprintf(w, "Команда: %s\n", strings.Join(info.Path, " "))
	return err
})
```

`Hidden` и `Deprecated` скрывают команду или flag из стандартного help. Запуск по имени остаётся доступен.

## Completion API

`app.Complete(ctx, words, opts...)` принимает слова после имени программы. Последнее слово считается редактируемым; для пустой позиции передайте `""`.
Метод возвращает `cli.Completion` с полями `Kind`, `Name`, `Prefix` и `Candidates`. Вывод кандидатов и shell integration остаются в вызывающем коде.

```go
result, err := app.Complete(ctx, []string{"deploy", "--env", "pr"})
if err != nil {
	return err
}
if result.Kind == cli.CompleteFlagValue {
	for _, candidate := range result.Candidates {
		fmt.Println(candidate.Value)
	}
}
```

Возможные значения `Kind`: `CompleteNone`, `CompleteCommand`, `CompleteFlagName`, `CompleteFlagValue`, `CompleteArg`.
Для значения flag поле `Name` содержит имя без `--`, `Prefix` содержит введённую часть значения. Например, для `-oj` у flag `-o` это `j`.
`Candidate.Value` тоже содержит только значение. Shell integration собирает итоговое слово.

`OneOf` даёт кандидатов для значения flag без отдельной функции.
Для динамических кандидатов задайте `.Complete(fn)` у `Flag`, `List`, `Map`, `Arg` или `Args`:

```go
b.Flag(&a.Env, "env").Complete(func(ctx context.Context, prefix string) ([]cli.Candidate, error) {
	return []cli.Candidate{
		{Value: "dev", Description: "Среда разработки"},
		{Value: "prod", Description: "Рабочая среда"},
	}, nil
})
```

Библиотека не фильтрует результат custom completion-функции по `prefix`. Фильтрацию добавьте в самой функции.
Введённые значения своей команды доступны через замыкание, значения предков через `cli.Parent[T](ctx)`.
Значения из окружения и default тоже разрешаются. Ошибки преобразования при частичном разборе пропускаются.

`Complete` не проверяет required flags и checks, не вызывает wrappers и обработчик.
Неизвестная команда или flag среди завершённых слов дают пустой результат без ошибки.
Ошибки схемы и custom completion-функции возвращаются вызывающему коду.
`Complete` и `Run` не должны вызываться одновременно на одном приложении.
