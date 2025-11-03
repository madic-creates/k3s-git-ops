# Kube DOOM: Kubernetes Chaos Engineering, 1993 Style

/// warning | DOOM: The DevOps Spin-Off Nobody Asked For
In 1993, id Software asked "What if we could kill demons with a shotgun?" In 2025, some absolute legend asked "What if we could kill pods with a shotgun?" And thus, Kube DOOM was born.
///

## What Fresh Hell Is This?

[Kube DOOM](https://github.com/storax/kubedoom){target=_blank} is a Kubernetes cluster management tool that transforms pod termination into a first-person shooter experience. Because apparently `kubectl delete pod` wasn't visceral enough.

Each pinky daemon in the game represents a pod in your cluster. When you frag a demon, the corresponding pod gets terminated. It's like chaos engineering, but with more pixelated blood and the iconic sound of a double-barreled shotgun.

Currently configured to target the `whoami` namespace, because apparently we need to answer life's fundamental question: "Who am I?" with "Someone who kills pods for fun."

/// tip | Therapeutic Pod Management
Studies show that 87% of SREs experience immediate stress relief when replacing `kubectl delete` with a BFG9000. The other 13% are lying.
///

## Why Does This Exist?

### The Philosophical Answer
Chaos engineering meets nostalgia. Resilience testing meets dopamine release. Your cluster meets the Doom Slayer.

### The Practical Answer
- **Visual Pod Management**: Sometimes you just want to *see* what you're killing
- **Stress Relief**: 3 AM incident? Kill some pods with style
- **Team Building**: Nothing says "DevOps culture" like a multiplayer deathmatch against your own infrastructure
- **Training**: Teach juniors about pod lifecycle management through the universal language of violence

### The Honest Answer
Because we can. Because it's hilarious. Because the cluster can handle it (hopefully).

/// danger | Production Use Warning
Should you run Kube DOOM in production? Technically, yes. Should you tell your manager? Absolutely not. Should you livestream it? Only if you've updated your LinkedIn profile recently.
///

## Installation Status

Kube DOOM is deployed to the `kubedoom` namespace with sync wave `86` (which, fun fact, is also the number of times you'll reload your shotgun before finding that one crashlooping pod).

**Target Namespace**: `whoami`

**ArgoCD Application**: `86-kube-doom.yaml`

## Getting Started: Your Journey Into Pod Purgatory

### Prerequisites
- VNC client (TigerVNC recommended, RealVNC if you're fancy)
- A cluster with pods you don't mind killing
- A sense of humor
- Questionable decision-making skills

### Step 1: Establish Port Forward

First, tunnel into the hellscape:

```bash
kubectl port-forward -n kubedoom service/kubedoom 5900:5900
```

/// info | Port Forwarding: The Gateway to Pod Hell
Port 5900 is the standard VNC port. It's also coincidentally the number of times you've thought "there has to be a better way" before discovering this tool.
///

### Step 2: Connect via VNC

Launch your VNC client and connect to:

```
127.0.0.1:5900
```

**VNC Password**: `idbehold`

/// tip | Authentication Comedy
The password is literally a DOOM cheat code. This is either brilliant security through obscurity or a cry for help from the developer.
///

### Step 3: Rip and Tear (Pods)

1. Use arrow keys or WASD to navigate
2. Spacebar to open doors (because even in pod management, some things require manual intervention)
3. Ctrl/Left-click to fire your weapon of choice
4. Kill demons to terminate pods
5. Watch in horror/delight as pods respawn (if controlled by a Deployment)
6. Question your career choices
7. Realize this is the most fun you've had at work in months

## Cheat Codes: Because Even Pod Management Needs Easy Mode

The classic DOOM cheats work in Kube DOOM, because why not add invincibility to cluster administration?

| Cheat Code | Effect | DevOps Translation |
|------------|--------|-------------------|
| `IDDQD` | God mode (invincibility) | You've achieved SRE enlightenment - nothing can page you now |
| `IDKFA` | All weapons and ammo | Full access to the Kubernetes API - use responsibly (narrator: they won't) |
| `IDSPISPOPD` | No-clip mode (walk through walls) | Network policies? Never heard of them |
| `IDCLEV` | Level select | Jump to different pod densities - try `IDCLEV31` for nightmare mode |
| `IDDT` | Full automap | Like `kubectl get pods --all-namespaces` but more dramatic |
| `IDCHOPPERS` | Chainsaw | For those intimate, close-range pod terminations |
| `IDCLIP` | No-clip mode (alternative) | RBAC is just a suggestion |

/// warning | With Great Cheats Comes Great Responsibility
Using `IDKFA` in production is like using `sudo rm -rf /` - you *can* do it, but your future self will have opinions.
///

### Activation Instructions

To enter cheat codes:
1. Simply type them during gameplay (no console needed)
2. Screen will flash briefly to confirm activation
3. Your conscience will flash warnings about production safety
4. Ignore both and proceed with extreme prejudice

## Technical Details (The Boring But Necessary Part)

### How It Works

1. Kube DOOM watches the target namespace (`whoami`) for pod events
2. Each pod is represented as a pinky daemon in the game
3. When you kill a monster, Kube DOOM executes `kubectl delete pod`
4. Pod controllers (Deployments, StatefulSets) will respawn pods - because true chaos never dies
5. You reload, respawn, and repeat

### Architecture

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│ VNC Client  │────────▶│  Kube DOOM   │────────▶│   Whoami    │
│  (You)      │  5900   │   Service    │  K8s API│  Namespace  │
└─────────────┘         └──────────────┘         └─────────────┘
      │                        │                         │
      │                        │                         │
      └──────[PEW PEW]───-─────┴────[kubectl delete]─────┘
```

### Resource Configuration

Kube DOOM is deployed via the upstream manifest from the [official repository](https://github.com/storax/kubedoom){target=_blank}.

**Namespace**: `kubedoom` - quarantined for everyone's safety

**Sync Wave**: `86` - deployed late enough that you have pods to kill

## Troubleshooting: When Things Go to Hell(spawn)

### Problem: I've Been Playing for 4 Hours

**Symptoms**: Complete loss of time awareness, questioning life choices

**Solutions**:
- This is working as intended
- Take a break
- Touch grass
- Remember you have actual production issues to fix
- Return in 10 minutes

## FAQ: Frequently Asked Questionable Decisions

### Q: Is this safe for production?

A: Define "safe." Define "production."

### Q: Can I target multiple namespaces?

A: Not in the current configuration. For multiple namespaces, you'd need to deploy multiple instances. Then you can have co-op multiplayer. Then you can explain to your manager why your monitoring shows "unusual pod churn."

### Q: What happens if I kill a database pod?

A: The same thing that happens when you `kubectl delete` a database pod, but with more explosions and the Doom Guy's grunting. StatefulSets will handle it. Probably. Maybe check your backups first.

### Q: Can I stream this?

A: Only if you're prepared to become either famous or unemployed. Possibly both.

### Q: My boss saw me playing this. Help?

A: "It's chaos engineering!" "We're testing pod resilience!" "This is active learning!" Or just update your resume. Your call.

### Q: Is this better than `kubectl delete pod`?

A: Objectively? No. Subjectively? Absolutely. Therapeutically? Without question.

## The Philosophy of Kube DOOM

In the end, Kube DOOM teaches us valuable lessons:

1. **Pods are ephemeral** - They live, they die, they respawn. Just like demons in Hell.
2. **Chaos builds resilience** - If your app can't survive random termination, it can't survive production.
3. **DevOps should be fun** - Who says cluster management has to be boring?
4. **Visual feedback matters** - Sometimes you need to *see* the destruction to feel alive.
5. **Nostalgia is powerful** - Combining 90s gaming with 2020s infrastructure is oddly satisfying.

/// quote | Ancient DevOps Proverb
"Rip and tear, until your cluster is stable." - Doom Guy, probably
///

**Remember**: In the grim darkness of the Kubernetes cluster, there is only DOOM.

---

/// danger | Final Warning
With great power comes great responsibility. With Kube DOOM comes great documentation for your incident post-mortem. Choose wisely.
///

*Now if you'll excuse me, I have some demons to kill in the `whoami` namespace. They know who they are.*
