# Installing deskrig, for an agent

You are an agent installing deskrig on the user's Mac. Do the steps in order. Ask the user only
where a step says to ask.

1. Install the binary, without the interactive setup:

       curl -fsSL https://raw.githubusercontent.com/sudiptadeb/deskrig/main/install.sh | DESKRIG_NO_SETUP=1 sh

   It goes to `~/.local/bin/deskrig`. If the installer says that folder isn't on PATH, call
   `~/.local/bin/deskrig` by its full path.

2. Check the desktop:

       deskrig status

   If `on console` is false or `screen locked` is true, tell the user that deskrig needs them
   logged in at the Mac's screen, unlocked (Screen Sharing works), and wait for them.

3. Permissions. Unless every permission in `status` is true, tell the user:

   > deskrig sets up every permission it may need once, one at a time; its updates keep them.
   > For each, System Settings opens on the right page: switch on deskrig, click Allow when
   > macOS asks, or drag deskrig from the Finder window that opens into the list.

   Then run it where the user can press Enter, or in the background if your shell has a short
   time limit (it then moves past the steps it can't check after a few seconds):

       deskrig setup permissions

   It ends with `permissions: all set`.

4. The skill, so later sessions know how to use deskrig:

       deskrig setup skill          # Claude Code: ~/.claude/skills/deskrig/SKILL.md
       deskrig skill                # prints it, for any other agent's instructions

5. VM testing. Ask the user:

   > Do you want a macOS VM target too? It downloads Tart and a macOS image, about 40 GB.

   If yes:

       deskrig setup vm             # in the background: the image takes a while; makes target "macos"
       deskrig vm start macos       # about a minute; prints `ready: ...`
       deskrig --target macos shot /tmp/vm.png    # look at it: the VM's desktop

6. Prove it works: `deskrig windows`, then `deskrig shot <file.png> "<an app from that list>"`,
   and look at the picture. Tell the user what you installed and that `deskrig help` lists the
   commands.
