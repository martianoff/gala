import * as path from 'path';
import { pathToFileURL } from 'url';
import { runTests } from '@vscode/test-electron';

async function main(): Promise<void> {
  const extensionDevelopmentPath = path.resolve(__dirname, '../../..');
  const extensionTestsPath = path.resolve(__dirname, './suite/index');
  const workspacePath = path.resolve(
    extensionDevelopmentPath,
    'src/test/fixtures/workspace',
  );

  const galaPath = process.env.GALA_BIN;
  if (galaPath) {
    console.log(`Using GALA binary: ${galaPath}`);
  } else {
    console.warn('GALA_BIN is not set; falling back to `gala` on PATH');
  }

  try {
    await runTests({
      extensionDevelopmentPath,
      extensionTestsPath,
      launchArgs: [
        '--folder-uri',
        pathToFileURL(workspacePath).toString(),
        '--disable-gpu',
        '--no-sandbox',
      ],
      extensionTestsEnv: {
        GALA_PATH: galaPath ?? '',
      },
    });
  } catch (error) {
    console.error('Extension tests failed:', error);
    process.exit(1);
  }
}

void main();
