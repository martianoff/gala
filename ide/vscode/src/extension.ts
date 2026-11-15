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
let galaFileWatcher: vscode.FileSystemWatcher | undefined;
// Every start/stop runs through this chain, so overlapping restarts (the
// command plus a gala.serverPath change) never leave a server running
// untracked.
let lifecycle: Promise<void> = Promise.resolve();

function serialized(step: () => Promise<void>): Promise<void> {
  lifecycle = lifecycle.then(step, step);
  return lifecycle;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function getOutputChannel(): vscode.LogOutputChannel {
  outputChannel ??= vscode.window.createOutputChannel('GALA Language Server', {
    log: true,
  });
  return outputChannel;
}

function resolveServerPath(): string {
  const config = vscode.workspace.getConfiguration('gala');
  const inspected = config.inspect<string>('serverPath');
  // A workspace's own setting picks the executable only once the workspace
  // is trusted (package.json lists gala.serverPath as restricted).
  const fromWorkspace = vscode.workspace.isTrusted
    ? inspected?.workspaceFolderValue ?? inspected?.workspaceValue
    : undefined;
  const explicit = fromWorkspace ?? inspected?.globalValue;
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
  const serverOptions: ServerOptions = serverExecutable();
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: 'file', language: 'gala' }],
    synchronize: {
      fileEvents: galaFileWatcher,
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

// Offers the fix for a failed start without waiting for the answer: a pending
// notification must not hold up activation or a restart.
function reportStartFailure(message: string): void {
  getOutputChannel().error(`Failed to start the GALA language server: ${message}`);
  void vscode.window
    .showErrorMessage(
      `GALA: cannot start the language server (${message}). ` +
        'Install the GALA CLI and check that `gala version` works in a terminal, ' +
        'or set "gala.serverPath".',
      'Download GALA',
      'Open Settings',
    )
    .then((choice) => {
      if (choice === 'Download GALA') {
        void vscode.env.openExternal(vscode.Uri.parse(RELEASES_URL));
      } else if (choice === 'Open Settings') {
        void vscode.commands.executeCommand(
          'workbench.action.openSettings',
          'gala.serverPath',
        );
      }
    });
}

async function startClient(): Promise<void> {
  client = createClient();
  try {
    await client.start();
  } catch (error) {
    reportStartFailure(errorMessage(error));
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
      `Stopping the GALA language server failed: ${errorMessage(error)}`,
    );
  }
}

function restartClient(): Promise<void> {
  return serialized(async () => {
    await stopClient();
    await startClient();
  });
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  galaFileWatcher = vscode.workspace.createFileSystemWatcher('**/*.gala');
  context.subscriptions.push(
    galaFileWatcher,
    vscode.commands.registerCommand('gala.restartServer', restartClient),
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration('gala.serverPath')) {
        await restartClient();
      }
    }),
  );
  await serialized(startClient);
}

export function deactivate(): Promise<void> {
  return serialized(stopClient);
}
