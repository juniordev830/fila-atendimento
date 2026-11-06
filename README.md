# Fila de Atendimento — MVP

Sistema local em **Go 1.22**, sem dependências externas, para emissão e chamada de senhas em ordem de chegada.

## Funcionalidades implementadas
- Emitir senhas sequenciais.
- Chamar a próxima senha aguardando em um guichê.
- Impedir que o mesmo guichê abra duas chamadas e que uma senha seja chamada por dois guichês.
- Encerrar como **atendida** ou **ausente**.
- Consultar fila e estados numa interface HTML local, acessível via navegador.

## Executar
```bash
go run .
```
Abra http://127.0.0.1:8080

## Testes
```bash
go test -race ./...
go vet ./...
```
O GitHub Actions executa testes e verificações em PRs e pushes.

## Limites do MVP
Os dados são mantidos **em memória** e desaparecem ao reiniciar. A primeira entrega utiliza Go + HTML padrão; **SQLite e HTMX** seguem como evolução, assim como autenticação, recuperação de senhas e relatórios. O servidor escuta somente `127.0.0.1`; não exponha esta aplicação diretamente à internet.

## Critério de aceite
Emitir duas senhas e chamar em dois guichês distintos deve respeitar a ordem de chegada, sem duplicar o atendimento.