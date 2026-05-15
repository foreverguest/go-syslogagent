SyslogAgent (Go) — port mínimo com suporte a serviço Windows, leitura do Event Log, agregação e logging local

Build

```powershell
cd SyslogAgent\go-syslogagent
go build -o syslogagent.exe
```

Instalação (Windows - como serviço)

Ao instalar, você pode gravar a configuração do servidor syslog e o intervalo de polling na Registry:

```powershell
# instala o serviço e salva o servidor + intervalo (em segundos)
.\syslogagent.exe -install -server 192.0.2.1:514 -poll 15
```

Remover serviço

```powershell
.\syslogagent.exe -remove
```

Execução em modo console (teste / leitura stdin):

```powershell
echo "2024-01-02 12:34:56 myproc This is a test" | .\syslogagent.exe -server 127.0.0.1:514

# executar poller localmente (poll a cada 5s)
.\syslogagent.exe -console -poll 5 -server 127.0.0.1:514 -debug

# ou lendo um arquivo
.\syslogagent.exe -file C:\path\to\sample.log -server 127.0.0.1:514 -debug
```

Funcionalidades principais

- Parser heurístico que normaliza a mensagem em uma única linha antes do envio.
- Envio via UDP para servidor syslog configurado (`-server` ou `HKLM\\SOFTWARE\\Syslog Agent\\SyslogServer`).
- Poller de Event Log (usa `wevtutil`) para `Application`, `System`, `Security` — envia novos eventos ao syslog.
- Agregação de fragmentos por `EventRecordID` (ou por `Provider+Time+Computer` quando EventRecordID não disponível) com janela configurada no código (`aggregateWindow`, default 500ms) para evitar envio fragmentado de um mesmo evento.
- Deduplicação: eventos processados são marcados e não reenviados repetidamente; o cache de eventos processados é persistido entre reinícios para evitar reenvio após restart.
- Serviço Windows instalável via `-install` (`github.com/kardianos/service`).
- Logging local em arquivo — caminho lido de `HKLM\\SOFTWARE\\Syslog Agent\\LogFilePath` ou `SyslogAgent.log` na pasta do executável.

Chaves de Registry usadas

- `HKLM\\SOFTWARE\\Syslog Agent\\SyslogServer` — servidor syslog (host:port)
- `HKLM\\SOFTWARE\\Syslog Agent\\EventLogPollInterval` — intervalo do poller (segundos)
- `HKLM\\SOFTWARE\\Syslog Agent\\LastEventPollTime` — timestamp RFC3339 do último evento processado (gravado pelo agente)
- `HKLM\\SOFTWARE\\Syslog Agent\\LogFilePath` — caminho para o arquivo de log local (opcional)
- `HKLM\\SOFTWARE\\Syslog Agent\\ProcessedCachePath` — (opcional) caminho para o arquivo de cache persistido de eventos processados (por padrão `processed_cache.json` ao lado do executável)

Exemplos de ajuste via PowerShell (Admin):

```powershell
# criar a chave e ajustar intervalo
New-Item -Path HKLM:\SOFTWARE\ -Name 'Syslog Agent' -Force
Set-ItemProperty -Path HKLM:\SOFTWARE\Syslog Agent -Name EventLogPollInterval -Value '15'
Set-ItemProperty -Path HKLM:\SOFTWARE\Syslog Agent -Name SyslogServer -Value '192.0.2.1:514'
Set-ItemProperty -Path HKLM:\SOFTWARE\Syslog Agent -Name LogFilePath -Value 'C:\Logs\syslogagent.log'
# opcional: alterar o local do cache persistido
Set-ItemProperty -Path HKLM:\SOFTWARE\Syslog Agent -Name ProcessedCachePath -Value 'C:\Logs\processed_cache.json'
```

Logs e depuração

- O agente grava mensagens de operação/erro no arquivo configurado (`LogFilePath`) ou `SyslogAgent.log` ao lado do binário.
- Use a opção `-debug` para imprimir no console os eventos encontrados e os envios de syslog.
- Para observar em tempo real:

```powershell
Get-Content .\SyslogAgent.log -Wait
```

Observabilidade adicional

- Sempre que um evento é agregado ou pulado por deduplicação o agente registra uma linha no log local (útil em debugging).

Notas e limitações

- O poller usa `wevtutil` (disponível por padrão no Windows) em vez de chamadas diretas à API do Event Log; isso simplifica o port e evita dependências C.
- O parser é heurístico e não garante compatibilidade total com todos os formatos do projeto C++ original; podemos estender para parity completa sob demanda.

Próximos passos sugeridos

- Ajustar `aggregateWindow` e `processedRetention` conforme seu ambiente (grandes volumes podem precisar de janelas maiores ou retenção menor).
- Adicionar filtros por `EventID`/nível, rotação de logs, ou suporte a TCP/RFC5424 conforme necessário.
