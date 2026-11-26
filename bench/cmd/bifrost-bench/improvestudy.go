package main

import (
	"context"

	"github.com/maximhq/bifrost/bench/improve"
)

// Improvement studies (D5 A4.1): the loop run several times from version 0,
// one after another, then one held-out experiment for all of them.

// CreateStudy freezes a study and starts it in the background.
func (s improveSource) CreateStudy(ctx context.Context, spec *improve.Spec, actor string) (*improve.StudyView, error) {
	if s.offline {
		return nil, improve.ErrOffline
	}
	st, err := s.r.CreateStudy(ctx, spec, actor)
	if err != nil {
		return nil, err
	}
	go func() {
		if _, err := s.r.RunStudy(context.Background(), st.ID); err != nil && s.r.Logger != nil {
			s.r.Logger.Warn("improvement study ended with an error", "study", st.ID, "error", err.Error())
		}
	}()
	v := s.r.ViewStudy(st)
	v.Running = true
	return &v, nil
}

// ListStudies lists the studies, newest first.
func (s improveSource) ListStudies(context.Context) (*improve.StudyList, error) {
	sts, err := s.r.ListStudies()
	if err != nil {
		return nil, err
	}
	l := &improve.StudyList{Studies: []improve.StudyView{}}
	for _, st := range sts {
		l.Studies = append(l.Studies, s.r.ViewStudy(st))
	}
	return l, nil
}

// GetStudy is one study as served.
func (s improveSource) GetStudy(_ context.Context, id string) (*improve.StudyView, error) {
	st, err := s.r.GetStudy(id)
	if err != nil {
		return nil, err
	}
	v := s.r.ViewStudy(st)
	return &v, nil
}

// StopStudy stops a study and its running loop.
func (s improveSource) StopStudy(_ context.Context, id, actor string) (*improve.StudyView, error) {
	st, err := s.r.StopStudy(id, actor)
	if err != nil {
		return nil, err
	}
	v := s.r.ViewStudy(st)
	return &v, nil
}
