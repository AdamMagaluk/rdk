// Package motionplan is a motion planning library.
package motionplan

import (
	"context"
	"math/rand"
	"sort"
	"time"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	commonpb "go.viam.com/api/common/v1"
	"go.viam.com/utils"

	frame "go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/robot"
	"go.viam.com/rdk/robot/framesystem"
	"go.viam.com/rdk/spatialmath"
)

// motionPlanner provides an interface to path planning methods, providing ways to request a path to be planned, and
// management of the constraints used to plan paths.
type motionPlanner interface {
	// Plan will take a context, a goal position, and an input start state and return a series of state waypoints which
	// should be visited in order to arrive at the goal while satisfying all constraints
	plan(context.Context, spatialmath.Pose, []frame.Input) ([][]frame.Input, error)

	// Everything below this point should be covered by anything that wraps the generic `planner`
	smoothPath(context.Context, []node) []node
	checkPath([]frame.Input, []frame.Input) bool
	checkInputs([]frame.Input) bool
	getSolutions(context.Context, spatialmath.Pose, []frame.Input) ([]*costNode, error)
	opt() *plannerOptions
}

type plannerConstructor func(frame.Frame, *rand.Rand, golog.Logger, *plannerOptions) (motionPlanner, error)

// PlanMotion plans a motion to destination for a given frame. It takes a given frame system, wraps it with a SolvableFS, and solves.
func PlanMotion(ctx context.Context,
	logger golog.Logger,
	dst *frame.PoseInFrame,
	f frame.Frame,
	seedMap map[string][]frame.Input,
	fs frame.FrameSystem,
	worldState *frame.WorldState,
	planningOpts map[string]interface{},
) ([]map[string][]frame.Input, error) {
	return motionPlanInternal(ctx, logger, []*frame.PoseInFrame{dst}, f, seedMap, fs, worldState, []map[string]interface{}{planningOpts})
}

// PlanRobotMotion plans a motion to destination for a given frame. A robot object is passed in and current position inputs are determined.
func PlanRobotMotion(ctx context.Context,
	dst *frame.PoseInFrame,
	f frame.Frame,
	r robot.Robot,
	fs frame.FrameSystem,
	worldState *frame.WorldState,
	planningOpts map[string]interface{},
) ([]map[string][]frame.Input, error) {
	seedMap, _, err := framesystem.RobotFsCurrentInputs(ctx, r, fs)
	if err != nil {
		return nil, err
	}

	return motionPlanInternal(ctx, r.Logger(), []*frame.PoseInFrame{dst}, f, seedMap, fs, worldState, []map[string]interface{}{planningOpts})
}

// PlanFrameMotion plans a motion to destination for a given frame with no frame system. It will create a new FS just for the plan.
// WorldState is not supported in the absence of a real frame system.
func PlanFrameMotion(ctx context.Context,
	logger golog.Logger,
	dst spatialmath.Pose,
	f frame.Frame,
	seed []frame.Input,
	planningOpts map[string]interface{},
) ([][]frame.Input, error) {
	// ephemerally create a framesystem containing just the frame for the solve
	fs := frame.NewEmptySimpleFrameSystem("")
	err := fs.AddFrame(f, fs.World())
	if err != nil {
		return nil, err
	}
	destination := frame.NewPoseInFrame(frame.World, dst)
	seedMap := map[string][]frame.Input{f.Name(): seed}
	solutionMap, err := motionPlanInternal(
		ctx,
		logger,
		[]*frame.PoseInFrame{destination},
		f,
		seedMap,
		fs,
		nil,
		[]map[string]interface{}{planningOpts},
	)
	if err != nil {
		return nil, err
	}
	return FrameStepsFromRobotPath(f.Name(), solutionMap)
}

// PlanWaypoints plans motions to a list of destinations in order for a given frame. It takes a given frame system, wraps it with a
// SolvableFS, and solves. It will generate a list of intermediate waypoints as well to pass to the solvable framesystem if possible.
func PlanWaypoints(ctx context.Context,
	logger golog.Logger,
	goals []*frame.PoseInFrame,
	f frame.Frame,
	seedMap map[string][]frame.Input,
	fs frame.FrameSystem,
	worldState *frame.WorldState,
	motionConfigs []map[string]interface{},
) ([]map[string][]frame.Input, error) {
	return motionPlanInternal(ctx, logger, goals, f, seedMap, fs, worldState, motionConfigs)
}

// motionPlanInternal is the internal private function that all motion planning access calls. This will construct the plan manager for each
// waypoint, and return at the end.
// This has the same function signature as `PlanWaypoints` but is a private function so as to not have public functions call other.
func motionPlanInternal(ctx context.Context,
	logger golog.Logger,
	goals []*frame.PoseInFrame,
	f frame.Frame,
	seedMap map[string][]frame.Input,
	fs frame.FrameSystem,
	worldState *frame.WorldState,
	motionConfigs []map[string]interface{},
) ([]map[string][]frame.Input, error) {
	if len(goals) == 0 {
		return nil, errors.New("no destinations passed to PlanWaypoints")
	}

	steps := make([]map[string][]frame.Input, 0, len(goals)*2)

	// Get parentage of solver frame. This will also verify the frame is in the frame system
	solveFrame := fs.Frame(f.Name())
	if solveFrame == nil {
		return nil, frame.NewFrameMissingError(f.Name())
	}
	solveFrameList, err := fs.TracebackFrame(solveFrame)
	if err != nil {
		return nil, err
	}

	opts := make([]map[string]interface{}, 0, len(goals))

	// If no planning opts, use default. If one, use for all goals. If one per goal, use respective option. Otherwise error.
	if len(motionConfigs) != len(goals) {
		switch len(motionConfigs) {
		case 0:
			for range goals {
				opts = append(opts, map[string]interface{}{})
			}
		case 1:
			// If one config passed, use it for all waypoints
			for range goals {
				opts = append(opts, motionConfigs[0])
			}
		default:
			return nil, errors.New("goals and motion configs had different lengths")
		}
	} else {
		opts = motionConfigs
	}

	// Each goal is a different PoseInFrame and so may have a different destination Frame. Since the motion can be solved from either end,
	// each goal is solved independently.
	for i, goal := range goals {
		// Create a frame to solve for, and an IK solver with that frame.
		sf, err := newSolverFrame(fs, solveFrameList, goal.Parent(), seedMap)
		if err != nil {
			return nil, err
		}
		if len(sf.DoF()) == 0 {
			return nil, errors.New("solver frame has no degrees of freedom, cannot perform inverse kinematics")
		}
		seed, err := sf.mapToSlice(seedMap)
		if err != nil {
			return nil, err
		}
		startPose, err := sf.Transform(seed)
		if err != nil {
			return nil, err
		}
		wsPb := &commonpb.WorldState{}
		if worldState != nil {
			wsPb, err = frame.WorldStateToProtobuf(worldState)
			if err != nil {
				return nil, err
			}
		}

		logger.Infof(
			"planning motion for frame %s. Goal: %v Starting seed map %v, startPose %v, worldstate: %v",
			f.Name(),
			frame.PoseInFrameToProtobuf(goal),
			seedMap,
			spatialmath.PoseToProtobuf(startPose),
			wsPb,
		)
		logger.Debugf("motion config for this step: %v", opts[i])

		sfPlanner, err := newPlanManager(sf, fs, logger, i)
		if err != nil {
			return nil, err
		}
		resultSlices, err := sfPlanner.PlanSingleWaypoint(ctx, seedMap, goal.Pose(), worldState, opts[i])
		if err != nil {
			return nil, err
		}
		for j, resultSlice := range resultSlices {
			stepMap := sf.sliceToMap(resultSlice)
			steps = append(steps, stepMap)
			if j == len(resultSlices)-1 {
				// update seed map
				seedMap = stepMap
			}
		}
	}

	logger.Debugf("final plan steps: %v", steps)

	return steps, nil
}

type planner struct {
	solver   InverseKinematics
	frame    frame.Frame
	logger   golog.Logger
	randseed *rand.Rand
	start    time.Time
	planOpts *plannerOptions
}

func newPlanner(frame frame.Frame, seed *rand.Rand, logger golog.Logger, opt *plannerOptions) (*planner, error) {
	ik, err := CreateCombinedIKSolver(frame, logger, opt.NumThreads)
	if err != nil {
		return nil, err
	}
	mp := &planner{
		solver:   ik,
		frame:    frame,
		logger:   logger,
		randseed: seed,
		planOpts: opt,
	}
	return mp, nil
}

func (mp *planner) checkInputs(inputs []frame.Input) bool {
	position, err := mp.frame.Transform(inputs)
	if err != nil {
		return false
	}
	ok, _, _ := mp.planOpts.CheckConstraints(&ConstraintInput{
		StartPos:   position,
		EndPos:     position,
		StartInput: inputs,
		EndInput:   inputs,
		Frame:      mp.frame,
	})
	return ok
}

func (mp *planner) checkPath(seedInputs, target []frame.Input) bool {
	ok, _ := mp.planOpts.CheckConstraintPath(
		&ConstraintInput{
			StartInput: seedInputs,
			EndInput:   target,
			Frame:      mp.frame,
		},
		mp.planOpts.Resolution,
	)
	return ok
}

func (mp *planner) opt() *plannerOptions {
	return mp.planOpts
}

// smoothPath will try to naively smooth the path by picking points partway between waypoints and seeing if it can interpolate
// directly between them. This will significantly improve paths from RRT*, as it will shortcut the randomly-selected configurations.
// This will only ever improve paths (or leave them untouched), and runs very quickly.
func (mp *planner) smoothPath(ctx context.Context, path []node) []node {
	mp.logger.Debugf("running simple smoother on path of len %d", len(path))
	if mp.planOpts == nil {
		mp.logger.Debug("nil opts, cannot shortcut")
		return path
	}
	if len(path) <= 2 {
		mp.logger.Debug("path too short, cannot shortcut")
		return path
	}

	// Randomly pick which quarter of motion to check from; this increases flexibility of smoothing.
	waypoints := []float64{0.25, 0.5, 0.75}

	for i := 0; i < mp.planOpts.SmoothIter; i++ {
		select {
		case <-ctx.Done():
			return path
		default:
		}
		// get start node of first edge. Cannot be either the last or second-to-last node.
		// Intn will return an int in the half-open interval half-open interval [0,n)
		firstEdge := mp.randseed.Intn(len(path) - 2)
		secondEdge := firstEdge + 1 + mp.randseed.Intn((len(path)-2)-firstEdge)
		mp.logger.Debugf("checking shortcut between nodes %d and %d", firstEdge, secondEdge+1)

		wayPoint1 := frame.InterpolateInputs(path[firstEdge].Q(), path[firstEdge+1].Q(), waypoints[mp.randseed.Intn(3)])
		wayPoint2 := frame.InterpolateInputs(path[secondEdge].Q(), path[secondEdge+1].Q(), waypoints[mp.randseed.Intn(3)])

		if mp.checkPath(wayPoint1, wayPoint2) {
			newpath := []node{}
			newpath = append(newpath, path[:firstEdge+1]...)
			newpath = append(newpath, &basicNode{wayPoint1}, &basicNode{wayPoint2})
			// have to split this up due to go compiler quirk where elipses operator can't be mixed with other vars in append
			newpath = append(newpath, path[secondEdge+1:]...)
			path = newpath
		}
	}
	return path
}

// getSolutions will initiate an IK solver for the given position and seed, collect solutions, and score them by constraints.
// If maxSolutions is positive, once that many solutions have been collected, the solver will terminate and return that many solutions.
// If minScore is positive, if a solution scoring below that amount is found, the solver will terminate and return that one solution.
func (mp *planner) getSolutions(ctx context.Context, goal spatialmath.Pose, seed []frame.Input) ([]*costNode, error) {
	// Linter doesn't properly handle loop labels
	nSolutions := mp.planOpts.MaxSolutions
	if nSolutions == 0 {
		nSolutions = defaultSolutionsToSeed
	}

	seedPos, err := mp.frame.Transform(seed)
	if err != nil {
		return nil, err
	}
	goalPos := fixOvIncrement(goal, seedPos)

	solutionGen := make(chan []frame.Input)
	ikErr := make(chan error, 1)
	defer func() { <-ikErr }()

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	// Spawn the IK solver to generate solutions until done
	utils.PanicCapturingGo(func() {
		defer close(ikErr)
		ikErr <- mp.solver.Solve(ctxWithCancel, solutionGen, goalPos, seed, mp.planOpts.metric, mp.randseed.Int())
	})

	solutions := map[float64][]frame.Input{}

	// A map keeping track of which constraints fail
	failures := map[string]int{}
	constraintFailCnt := 0

	// Solve the IK solver. Loop labels are required because `break` etc in a `select` will break only the `select`.
IK:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		select {
		case step := <-solutionGen:
			cPass, cScore, failName := mp.planOpts.CheckConstraints(&ConstraintInput{
				seedPos,
				goalPos,
				seed,
				step,
				mp.frame,
			})
			if cPass {
				// TODO (pl): Current implementation of constraints treats the starting input of a ConstraintInput as the state to check for
				// validity. Since we use CheckConstraints instead of CheckConstraintPath here, we need to check both the start and
				// end pose for validity
				endPass, _, failName := mp.planOpts.CheckConstraints(&ConstraintInput{
					goalPos,
					goalPos,
					step,
					step,
					mp.frame,
				})

				if endPass {
					if cScore < mp.planOpts.MinScore && mp.planOpts.MinScore > 0 {
						solutions = map[float64][]frame.Input{}
						solutions[cScore] = step
						// good solution, stopping early
						break IK
					}

					solutions[cScore] = step
					if len(solutions) >= nSolutions {
						// sufficient solutions found, stopping early
						break IK
					}
				} else {
					constraintFailCnt++
					failures[failName]++
				}
			} else {
				constraintFailCnt++
				failures[failName]++
			}
			// Skip the return check below until we have nothing left to read from solutionGen
			continue IK
		default:
		}

		select {
		case <-ikErr:
			// If we have a return from the IK solver, there are no more solutions, so we finish processing above
			// until we've drained the channel
			break IK
		default:
		}
	}
	if len(solutions) == 0 {
		// We have failed to produce a usable IK solution. Let the user know if zero IK solutions were produced, or if non-zero solutions
		// were produced, which constraints were failed
		if constraintFailCnt == 0 {
			return nil, errIKSolve
		}

		return nil, genIKConstraintErr(failures, constraintFailCnt)
	}

	keys := make([]float64, 0, len(solutions))
	for k := range solutions {
		keys = append(keys, k)
	}
	sort.Float64s(keys)

	orderedSolutions := make([]*costNode, 0)
	for _, key := range keys {
		orderedSolutions = append(orderedSolutions, newCostNode(solutions[key], key))
	}
	return orderedSolutions, nil
}
