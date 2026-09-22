import * as vscode from 'vscode';
import {
  Executable,
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from 'vscode-languageclient/node';

const RELEASES_URL = 'https://github.com/martianoff/gala/releases';

let client: LanguageClient | undefined;
let outputChannel: vscode.LogOutputChannel | undefined;

function getOutputChannel(): vscode.LogOutputChannel {
  outputChannel ??= vscode.window.createOutputChannel('GALA Language Server', {
    log: true,
  });
  return outputChannel;
}

function resolveServerPath(): string {
  const config = vscode.workspace.getConfiguration('gala');
  const inspected = config.inspect<string>('serverPath');
  const explicit =
    inspected?.workspaceFolderValue ??
    inspected?.workspaceValue ??
    inspected?.globalValue;
  if (typeof explicit === 'string' && explicit.trim()) {
    return explicit.trim();
  }
  const fromEnv = process.env.GALA_PATH?.trim();
  return fromEnv || config.get<string>('serverPath')?.trim() || 'gala';
}

function serverExecutable(): Executable {
  return {
    command: resolveServerPath(),
    args: ['lsp'],
    options: {
      cwd: vscode.workspace.workspaceFolders?.[0]?.uri.fsPath,
    },
  };
}

function createClient(): LanguageClient {
  const serverOptions: ServerOptions = {
    run: serverExecutable(),
    debug: serverExecutable(),
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: 'file', language: 'gala' }],
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher('**/*.gala'),
    },
    outputChannel: getOutputChannel(),
  };
  return new LanguageClient(
    'gala',
    'GALA Language Server',
    serverOptions,
    clientOptions,
  );
}

async function startClient(): Promise<void> {
  client = createClient();
  try {
    await client.start();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    getOutputChannel().error(`Failed to start the GALA language server: ${message}`);
    const choice = await vscode.window.showErrorMessage(
      `GALA: cannot start the language server (${message}). ` +
        'Install the GALA CLI and check that `gala version` works in a terminal, ' +
        'or set "gala.serverPath".',
      'Download GALA',
      'Open Settings',
    );
    if (choice === 'Download GALA') {
      void vscode.env.openExternal(vscode.Uri.parse(RELEASES_URL));
    } else if (choice === 'Open Settings') {
      void vscode.commands.executeCommand(
        'workbench.action.openSettings',
        'gala.serverPath',
      );
    }
  }
}

async function stopClient(): Promise<void> {
  const running = client;
  client = undefined;
  if (!running) {
    return;
  }
  try {
    await running.stop();
  } catch (error) {
    getOutputChannel().warn(
      `Stopping the GALA language server failed: ${
        error instanceof Error ? error.message : String(error)
      }`,
    );
  }
}

async function restartClient(): Promise<void> {
  await stopClient();
  await startClient();
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  context.subscriptions.push(
    vscode.commands.registerCommand('gala.restartServer', restartClient),
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration('gala.serverPath')) {
        await restartClient();
      }
    }),
  );
  await startClient();
}

export async function deactivate(): Promise<void> {
  await stopClient();
}
