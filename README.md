# Fila de Atendimento

MVP de senhas para pequenos balcoes de atendimento, implementado em **Go 1.23**, apenas com a biblioteca padrao.

## Funcionalidades

- Emite senhas sequenciais por ordem de chegada.
- Chama a primeira senha em espera para um guiche informado.
- Nao permite que uma senha seja atendida em dois guiches, nem que o mesmo guiche abra dois atendimentos simultaneos.
- Finaliza o atendimento ou registra ausencia, com historico consultavel em memoria.
- API HTTP JSON, validacao de metodos e testes concorrentes.

## Executar

```bash
go run .
```

O servidor fica em `http://127.0.0.1:8080` por padrao. Configure `ADDR` para alterar a escuta, por exemplo `ADDR=127.0.0.1:9090 go run .`.

```bash
curl -X POST http://127.0.0.1:8080/tickets
curl -X POST http://127.0.0.1:8080/tickets
curl -X POST http://127.0.0.1:8080/call   -H 'Content-Type: application/json' -d '{"desk":"guiche-1"}'
curl -X POST http://127.0.0.1:8080/resolve   -H 'Content-Type: application/json' -d '{"id":1,"status":"completed"}'
curl http://127.0.0.1:8080/tickets
```

Em `/resolve`, status permitido: `completed` ou `absent`. Chamadas invalidas retornam 400, e conflitos de estado retornam 409.

## Validacao

```bash
go test -race ./...
go vet ./...
```

O workflow CI executa os dois comandos em PRs. A verificacao de concorrencia usa mutex no mesmo processo.

## Limites

- **Armazenamento somente em memoria**: reiniciar perde as senhas; persistencia SQLite e recuperacao apos falhas estao no backlog.
- **Sem login nem autenticacao**: API deve permanecer local, nunca publica na internet sem autorizacao e protecoes apropriadas.
- Nao ha garantia de exclusao mutua entre *multiplos processos*. Usar um processo unico nesta primeira versao.
- Ainda nao possui HTMX, frontend ou painel de atendimento.
