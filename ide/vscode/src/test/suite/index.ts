import * as assert from 'assert';
import * as vscode from 'vscode';

const DIAGNOSTICS_TIMEOUT_MS = 120_000;
const POLL_INTERVAL_MS = 500;

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fixtureUri(...segments: string[]): vscode.Uri {
  const root = vscode.workspace.workspaceFolders?.[0]?.uri;
  if (root) {
    return vscode.Uri.joinPath(root, ...segments);
  }
  const extension = vscode.extensions.getExtension('martianoff.gala');
  if (!extension) {
    throw new Error('the martianoff.gala extension is not installed');
  }
  return vscode.Uri.joinPath(
    extension.extensionUri,
    'src/test/fixtures/workspace',
    ...segments,
  );
}

async function waitForDiagnostics(
  uri: vscode.Uri,
): Promise<vscode.Diagnostic[]> {
  const deadline = Date.now() + DIAGNOSTICS_TIMEOUT_MS;
  let seen: vscode.Diagnostic[] = [];
  while (Date.now() < deadline) {
    seen = vscode.languages.getDiagnostics(uri);
    if (seen.length > 0) {
      return seen;
    }
    await delay(POLL_INTERVAL_MS);
  }
  return seen;
}

export async function run(): Promise<void> {
  console.log(
    `workspaceFolders=${JSON.stringify(
      vscode.workspace.workspaceFolders?.map((f) => f.uri.toString()) ?? [],
    )}`,
  );
  const extension = vscode.extensions.getExtension('martianoff.gala');
  assert.ok(extension, 'martianoff.gala extension is not installed');
  await extension.activate();

  const commands = await vscode.commands.getCommands(true);
  assert.ok(
    commands.includes('gala.restartServer'),
    'gala.restartServer command is not registered',
  );

  const uri = fixtureUri('main.gala');
  const document = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(document);

  assert.strictEqual(
    document.languageId,
    'gala',
    '.gala files must be associated with the gala language',
  );

  const galaPath = process.env.GALA_PATH?.trim();
  if (!galaPath) {
    console.warn('GALA_PATH not set; skipping language server assertions');
    return;
  }

  const diagnostics = await waitForDiagnostics(uri);
  assert.ok(
    diagnostics.length > 0,
    `gala lsp published no diagnostics within ${DIAGNOSTICS_TIMEOUT_MS}ms`,
  );

  const messages = diagnostics.map((d) => d.message).join('\n');
  console.log(`Received diagnostics:\n${messages}`);
}
