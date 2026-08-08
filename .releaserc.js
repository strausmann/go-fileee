module.exports = {
  branches: ["main"],
  repositoryUrl: "git@github.com:strausmann/go-fileee.git",
  plugins: [
    [
      "@semantic-release/commit-analyzer",
      {
        preset: "conventionalcommits",
        releaseRules: [
          // Vor 1.0 sind Breaking-Changes SemVer-konform ein Minor-Bump, kein Major-Sprung
          // (0.x darf sich jederzeit brechend ändern, ohne dass das "1.0-Reife" signalisiert).
          // OHNE diese Regel würde jeder Commit mit "!"/"BREAKING CHANGE:"-Marker auf die
          // Standardregel `{breaking:true, release:"major"}` durchfallen (Custom-Rules und
          // Default-Rules werden von @semantic-release/commit-analyzer getrennt geprüft --
          // ein Match in den Custom-Rules verhindert den Rückfall auf die Default-Rules).
          // Muss zwingend VOR den beiden `patch`-Regeln unten stehen: die Prüfung nimmt die
          // erste Regel, die matcht, und ein breaking `refactor!`/`build!`-Commit soll trotzdem
          // `minor` werden, nicht `patch`.
          //
          // WICHTIG bei Erreichen von v1.0.0: diese Regel entfernen (oder auf
          // `release: "major"` ändern) -- ab 1.0 sollen Breaking-Changes wieder einen
          // Major-Bump auslösen. Empirisch verifiziert (nicht nur aus dem Quelltext
          // geschlossen) im Rahmen von go-fileee#36.
          { breaking: true, release: "minor" },
          { type: "refactor", release: "patch" },
          { type: "build", release: "patch" },
        ],
      },
    ],
    ["@semantic-release/release-notes-generator", { preset: "conventionalcommits" }],
    ["@semantic-release/changelog", { changelogFile: "CHANGELOG.md" }],
    [
      "@semantic-release/git",
      {
        assets: ["CHANGELOG.md"],
        message: "chore(release): ${nextRelease.version} [skip ci]\n\n${nextRelease.notes}",
      },
    ],
    "@semantic-release/github",
  ],
};
