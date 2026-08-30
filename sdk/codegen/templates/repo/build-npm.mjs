#!/usr/bin/env node
// Builds sdk/typescript for npm publish (UBI-143: JSR -> npm migration).
// The checked-in sdk/typescript/package.json is `ubx sdk gen` output -- a
// stub, "DO NOT EDIT" -- never the real publishable manifest. This script
// never touches it; it compiles fresh into dist/ and writes a REAL
// package.json there, derived from deno.json's own name/version/exports
// (already the single source of truth for what this package exposes).
//
// Env:
//   UBX_SDK_RUNTIME_VERSION  -- the @ubx/sdk version this build depends on
//                                (real npm dependency, never vendored).
import { execSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync, rmSync, cpSync } from "node:fs";
import { dirname, join } from "node:path";

const root = "sdk/typescript";
const runtimeVersion = process.env.UBX_SDK_RUNTIME_VERSION;
if (!runtimeVersion) {
  console.error("UBX_SDK_RUNTIME_VERSION not set");
  process.exit(1);
}

const denoJson = JSON.parse(readFileSync(join(root, "deno.json"), "utf8"));
const { name, version, license, exports: exportMap } = denoJson;
if (!name || !version || !exportMap) {
  console.error("deno.json missing name/version/exports");
  process.exit(1);
}

// 1. Real dependency resolution -- installs the ACTUAL @ubx/sdk package
//    (never a local stub) so tsc type-checks the type-only import for
//    real, the same way a downstream consumer will.
//
// npm install mutates the real, committed package.json as a side
// effect even with --no-save (confirmed live, UBI-185: a bare
// rmSync here -- this script's own original version -- staged a real
// DELETION of the committed stub once the full publish pipeline
// finally ran successfully end to end for the first time, since
// nothing downstream ever restored it before "Commit version bump"
// git-added whatever was left on disk). Saved and restored verbatim
// below instead of discarded -- this file is real, checked-in
// scaffolding (`ubx sdk gen` output, kept in sync by the workflow's
// own earlier version-bump step), not a build artifact.
const originalPackageJson = readFileSync(join(root, "package.json"), "utf8");
rmSync(join(root, "node_modules"), { recursive: true, force: true });
writeFileSync(
  join(root, "package.json.build-tmp"),
  JSON.stringify({ name: "build-scratch", private: true, dependencies: { "@ubx/sdk": runtimeVersion } }, null, 2),
);
execSync(`npm install --prefix ${root} --package-lock=false --no-save --silent ${JSON.stringify(`@ubx/sdk@${runtimeVersion}`)}`, {
  stdio: "inherit",
});
rmSync(join(root, "package.json.build-tmp"), { force: true });
writeFileSync(join(root, "package.json"), originalPackageJson);

// 2. Compile: isolatedDeclarations, one .js + one .d.ts per source file --
//    no bundling, matches deno.json's own one-export-per-file shape.
rmSync(join(root, "dist"), { recursive: true, force: true });
const tsconfigPath = join(root, "tsconfig.npm-build.json");
writeFileSync(
  tsconfigPath,
  JSON.stringify(
    {
      compilerOptions: {
        target: "ES2022",
        module: "ES2022",
        moduleResolution: "bundler",
        declaration: true,
        isolatedDeclarations: true,
        isolatedModules: true,
        skipLibCheck: true,
        strict: false,
        outDir: "dist",
        rootDir: ".",
      },
      include: ["**/*.ts"],
      exclude: ["dist/**", "node_modules/**"],
    },
    null,
    2,
  ),
);
execSync(`npx --yes typescript@7.0.2 --project ${tsconfigPath}`, { stdio: "inherit" });
rmSync(tsconfigPath, { force: true });

// 3. Real, publishable package.json into dist/ -- exports map is deno.json's
//    own (identical keys), each value rewritten from "./x.ts" to the
//    compiled { types, default } pair. No manual enumeration: any resource
//    deno.json already lists, npm's manifest lists identically.
const npmExports = {};
for (const [subpath, tsRelPath] of Object.entries(exportMap)) {
  const stem = tsRelPath.replace(/^\.\//, "").replace(/\.ts$/, "");
  npmExports[subpath] = { types: `./${stem}.d.ts`, default: `./${stem}.js` };
}
// npm's provenance verification (publish.yml's own `--provenance` flag)
// cross-checks this package.json's own repository.url against the
// REAL GitHub Actions run's own repo -- confirmed live, a real E422
// ("Error verifying sigstore provenance bundle: Failed to validate
// repository information: package.json: repository.url is ''") from
// dist/package.json never carrying one at all until now. @ubx/sdk
// (ubx-sdk-typescript, published successfully first) already commits
// this field directly in its own checked-in package.json; the six
// provider repos' own dist/package.json is built fresh here instead,
// so it needs writing here, not copied from a stub that deliberately
// carries none of the real publishable fields.
const repoSlug = name === "@ubx/sdk" ? "ubx-sdk-typescript" : `ubx-sdk-${name.split("/")[1].replace(/^sdk-/, "")}`;
const pkg = {
  name,
  version,
  license: license || "Apache-2.0",
  type: "module",
  repository: { type: "git", url: `git+https://github.com/Ubiquex/${repoSlug}.git` },
  exports: npmExports,
  dependencies: { "@ubx/sdk": runtimeVersion },
};
writeFileSync(join(root, "dist", "package.json"), JSON.stringify(pkg, null, 2) + "\n");

console.log(`built ${Object.keys(npmExports).length} exports -> ${root}/dist/package.json (${name}@${version})`);
